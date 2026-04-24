// Command deliverer is the pulsar-notifcation-pipeline's reader/deliverer
// service (Constitution Principle III: reader/deliverer = Pulsar → push).
// It consumes encrypted messages from Apache Pulsar, decrypts with the
// configured private key(s), dispatches to the configured PushProvider, and
// emits a terminal outcome for every message.
package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/deliverer/config"
	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/deliverer/consumer"
	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/deliverer/dispatcher"
	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/deliverer/outcomes"
	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/deliverer/provider"
	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/deliverer/provider/pushover"
	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/deliverer/provider/sandbox"
	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/deliverer/retry"
	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/logging"
	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/pulsarlib"
	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/telemetry"
)

func main() {
	if err := run(); err != nil {
		_, _ = os.Stderr.WriteString("deliverer: " + err.Error() + "\n")
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	logger, err := logging.New(os.Stdout, cfg.LogLevel)
	if err != nil {
		return err
	}
	slog.SetDefault(logger)
	logger.Info("deliverer.starting",
		slog.String("provider", string(cfg.Provider)),
		slog.String("pulsar_service_url", cfg.PulsarServiceURL),
		slog.String("pulsar_topic", cfg.PulsarTopic),
		slog.String("pulsar_subscription", cfg.PulsarSubscription),
		slog.Int("concurrency", cfg.Concurrency),
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

	push, err := buildProvider(cfg)
	if err != nil {
		return err
	}

	reader, err := pulsarlib.NewApacheConsumer(
		pulsarlib.WithServiceURL(cfg.PulsarServiceURL),
		pulsarlib.WithTopic(cfg.PulsarTopic),
		pulsarlib.WithSubscription(cfg.PulsarSubscription),
		pulsarlib.WithEncryptionKeyReader(pulsarlib.NewFileCryptoKeyReader(cfg.PulsarEncryptionKeyDir)),
		pulsarlib.WithLogger(logger),
	)
	if err != nil {
		return err
	}
	defer func() { _ = reader.Close() }()

	recorder := outcomes.NewSlogRecorder(outcomes.WithLogger(logger))
	retryPolicy := retry.NewPolicy(
		retry.WithMaxAttempts(cfg.RetryMaxAttempts),
		retry.WithInitialBackoff(cfg.RetryInitialBackoff),
		retry.WithMaxBackoff(cfg.RetryMaxBackoff),
	)
	// Register the configured provider under the Notification.target oneof
	// arm it serves. At MVP only the pushover arm exists; the sandbox
	// provider is a *substitute implementation* for that same arm, not a
	// separate arm. Adding a new oneof arm in the protobuf is the only way
	// to have multiple live arms.
	disp, err := dispatcher.New(
		dispatcher.WithProvider("pushover", push),
		dispatcher.WithRecorder(recorder),
		dispatcher.WithRetryPolicy(retryPolicy),
	)
	if err != nil {
		return err
	}

	cons, err := consumer.New(
		consumer.WithReader(reader),
		consumer.WithDispatcher(disp),
		consumer.WithRecorder(recorder),
		consumer.WithLogger(logger),
	)
	if err != nil {
		return err
	}

	// Liveness endpoint is served by ootel at http://<host>:MetricsPort/healthcheck
	// (regardless of exporter type). We do not bind the port ourselves —
	// doing so would race ootel's internal HTTP server.

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Spawn N consumer workers all sharing the same subscription (Shared
	// subscription type in pulsarlib — load-balanced across workers).
	var wg sync.WaitGroup
	for i := 0; i < cfg.Concurrency; i++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			if err := cons.Run(ctx); err != nil {
				logger.Error("deliverer.worker.error", slog.Int("worker", worker), slog.Any("err", err))
			}
		}(i)
	}

	<-ctx.Done()
	logger.Info("deliverer.shutdown.beginning")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownGracePeriod)
	defer cancel()
	_ = shutdownCtx // reserved for future shutdown work alongside wg.Wait below

	wg.Wait()
	logger.Info("deliverer.shutdown.complete")
	time.Sleep(10 * time.Millisecond)
	return nil
}

func buildProvider(cfg *config.Config) (provider.PushProvider, error) {
	switch cfg.Provider {
	case config.ProviderPushover:
		return pushover.New(
			pushover.WithAppToken(cfg.PushoverAppToken),
			pushover.WithBaseURL(cfg.PushoverBaseURL),
		)
	case config.ProviderSandbox:
		return sandbox.New(), nil
	default:
		return nil, errors.New("deliverer: unknown provider " + string(cfg.Provider))
	}
}
