package main

import (
	"flag"

	"gitlab.com/akita/mgpusim/benchmarks/microbenchmarks/l2bandwidth"
	"gitlab.com/akita/mgpusim/samples/runner"
)

func main() {
	flag.Parse()

	runner := new(runner.Runner).ParseFlag().Init()

	benchmark := l2bandwidth.NewBenchmark(runner.GPUDriver)

	runner.AddBenchmark(benchmark)

	runner.Run()
}
