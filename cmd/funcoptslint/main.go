// Command funcoptslint is the singlechecker wrapper around the funcoptslint
// analyzer (Constitution Principle II enforcement). Invoke it as a go-vet
// style tool:
//
//	go run ./cmd/funcoptslint ./...
package main

import (
	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/tools/funcoptslint"
	"golang.org/x/tools/go/analysis/singlechecker"
)

func main() {
	singlechecker.Main(funcoptslint.Analyzer)
}
