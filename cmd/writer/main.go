// Command writer is the pulsar-notifcation-pipeline's inbound HTTP service
// (Constitution Principle III: writer = API → Pulsar). It accepts notifications
// over HTTP (protobuf or JSON bodies), validates them, assigns server-
// controlled fields, and publishes the ciphertext to Apache Pulsar using
// Pulsar native E2EE.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/logging"
	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/pulsarlib"
	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/telemetry"
	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/writer/auth"
	writerconfig "github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/writer/config"
	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/writer/httpserver"
	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/writer/ingest"
)

func main() {
	if err := run(); err != nil {
		// main.go is the single place where we may emit to os.Stderr because
		// slog hasn't been initialised yet. Nowhere else in the codebase.
		_, _ = os.Stderr.WriteString("writer: " + err.Error() + "\n")
		os.Exit(1)
	}
}

func run() error {
	cfg, err := writerconfig.Load()
	if err != nil {
		return err
	}
	logger, err := logging.New(os.Stdout, cfg.LogLevel)
	if err != nil {
		return err
	}
	slog.SetDefault(logger)
	logger.Info("writer.starting",
		slog.String("http_addr", cfg.HTTPAddr),
		slog.String("pulsar_service_url", cfg.PulsarServiceURL),
		slog.String("pulsar_topic", cfg.PulsarTopic),
		slog.Any("encryption_key_names", cfg.EncryptionKeyNames()),
	)

	telShutdown, err := telemetry.Init(context.Background(), telemetry.Config{
		ServiceName:       cfg.TracingService,
		ServiceVersion:    cfg.TracingVersion,
		MetricsEnabled:    cfg.MetricsEnabled,
		MetricsPort:       cfg.MetricsPort,
		TracingEnabled:    cfg.TracingEnabled,
		TracingSampleRate: cfg.TracingSampleRate,
		Local:             cfg.Local,
	})
	if err != nil {
		return err
	}
	defer func() {
		sctx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownGracePeriod)
		defer cancel()
		_ = telShutdown(sctx)
	}()

	if _, err := pulsarlib.LoadKeySet(cfg.PulsarEncryptionKeyDir, cfg.EncryptionKeyNames()); err != nil {
		return err
	}

	bearer, err := auth.NewBearer(auth.WithTokensFile(cfg.BearerTokensFile))
	if err != nil {
		return err
	}

	publisher, err := pulsarlib.NewApachePublisher(
		pulsarlib.WithServiceURL(cfg.PulsarServiceURL),
		pulsarlib.WithTopic(cfg.PulsarTopic),
		pulsarlib.WithEncryptionKeyReader(pulsarlib.NewFileCryptoKeyReader(cfg.PulsarEncryptionKeyDir)),
		pulsarlib.WithEncryptionKeyNames(cfg.EncryptionKeyNames()...),
		pulsarlib.WithLogger(logger),
	)
	if err != nil {
		return err
	}
	defer func() { _ = publisher.Close() }()

	ingester, err := ingest.NewIngester(ingest.WithPublisher(publisher))
	if err != nil {
		return err
	}

	handlers, err := httpserver.NewHandlers(
		httpserver.WithIngester(ingester),
		httpserver.WithRequestIDExtractor(func(r *http.Request) string {
			return httpserver.RequestIDFromContext(r.Context())
		}),
	)
	if err != nil {
		return err
	}

	server, err := httpserver.NewServer(
		httpserver.WithHandler(handlers),
		httpserver.WithAuthenticator(bearer),
		httpserver.WithLogger(logger),
		httpserver.WithAddress(cfg.HTTPAddr),
	)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		logger.Info("writer.http.listening", slog.String("addr", server.Addr()))
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		logger.Info("writer.shutdown.beginning")
	case err := <-errCh:
		return err
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownGracePeriod)
	defer cancel()
	_ = server.Shutdown(shutdownCtx)
	logger.Info("writer.shutdown.complete")
	// Small pause so observability flushes.
	time.Sleep(10 * time.Millisecond)
	return nil
}
