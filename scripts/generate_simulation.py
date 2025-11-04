#!/usr/bin/python3
import os
import subprocess
import argparse
import datetime
import shutil
from plot.benchmark import memory_overhead

# ==========================================================
# Configuration
# ==========================================================

CachePWQ_PATH = "/Users/chenzihang/codes/CachePWQ"
MGPU_PATH = f"{CachePWQ_PATH}/simulator/mgpusim/"
BENCH_PATH = os.path.join(MGPU_PATH, "samples")
CONFIG = "monolithic"

BENCHMARKS = [
    "convolution2d",
    "fastwalshtransform",
    "gups",
    "jacobi1d",
    "jacobi2d",
    "kmeans",
    "matrixtranspose",
    "mis",
    "pagerank",
    "simpleconvolution",
    "shoc-reduction",
    "spmv",
    "stencil2d",
    "syrk",
    "syr2k",
]

# ==========================================================
# Compile Phase
# ==========================================================


class Benchmark:
    """Helper class for compiling one benchmark"""

    def __init__(self, name):
        self.name = name
        self.path = os.path.join(BENCH_PATH, name)
        self.binary_path = os.path.join(self.path, name)

    def compile(self):
        with open(os.devnull, "w") as fp:
            p = subprocess.Popen(
                "go build", shell=True, cwd=self.path, stdout=fp, stderr=fp
            )
            p.wait()
        return p.returncode == 0


def clean_all():
    print("[Cleaning all benchmarks...]")
    for name in BENCHMARKS:
        bench = Benchmark(name)
        if os.path.exists(bench.binary_path):
            os.remove(bench.binary_path)
    print("[Clean] All benchmarks cleaned.")


def compile_all():
    print("[Compiling all benchmarks...]")
    failed = []
    for name in BENCHMARKS:
        bench = Benchmark(name)
        ok = bench.compile()
        if not ok:
            failed.append(name)

    if failed:
        print(f"[ERROR] Failed to compile: {', '.join(failed)}")
    else:
        print("[Compile] All benchmarks compiled successfully.")


# ==========================================================
# Runner Script Generation Phase
# ==========================================================


def find_optimal_combination(memory_limit_mb):
    tasks = list(memory_overhead.items())
    n = len(tasks)

    # 动态规划表
    dp = [[0] * (memory_limit_mb + 1) for _ in range(n + 1)]
    selected = [[False] * (memory_limit_mb + 1) for _ in range(n + 1)]

    for i in range(1, n + 1):
        task_name, task_memory = tasks[i - 1]
        for j in range(memory_limit_mb + 1):
            if task_memory <= j:
                if dp[i - 1][j] < dp[i - 1][j - task_memory] + 1:
                    dp[i][j] = dp[i - 1][j - task_memory] + 1
                    selected[i][j] = True
                else:
                    dp[i][j] = dp[i - 1][j]
                    selected[i][j] = False
            else:
                dp[i][j] = dp[i - 1][j]
                selected[i][j] = False

    result_tasks = []
    remaining_memory = memory_limit_mb
    for i in range(n, 0, -1):
        if selected[i][remaining_memory]:
            result_tasks.append(tasks[i - 1][0])
            remaining_memory -= tasks[i - 1][1]

    total_memory_used = memory_limit_mb - remaining_memory
    memory_utilization = total_memory_used / memory_limit_mb

    return result_tasks, total_memory_used, memory_utilization


def generate_runners_on_desktop(yaml_path=None):
    timestamp = datetime.datetime.now().strftime("%Y-%m-%d_%H-%M-%S")
    base_output_dir = os.path.join("../runs", timestamp)
    os.makedirs(base_output_dir, exist_ok=True)

    tasks, total_mem, util = find_optimal_combination(48 * 1024)  # 48 GB

    print(
        f"[Info] Selected {len(tasks)} benchmarks with total memory {total_mem} MB (Utilization: {util*100:.2f}%)"
    )

    for benchmark in BENCHMARKS:
        # --- Create benchmark-specific folder ---
        bench_dir = os.path.join(base_output_dir, benchmark)
        os.makedirs(bench_dir, exist_ok=True)

        # --- Move compiled binary ---
        src_bin = os.path.join(BENCH_PATH, benchmark, benchmark)
        dst_bin = os.path.join(bench_dir, benchmark)
        if os.path.exists(src_bin):
            shutil.move(src_bin, dst_bin)
        else:
            print(f"[WARN] No binary found for {benchmark}")

        # --- Generate runner script ---
        file_path = os.path.join(bench_dir, f"{benchmark}.sh")
        with open(file_path, "w") as f:
            # Normal local execution
            f.write("#!/bin/bash\n")
            f.write("set -e\n")

            f.write(
                f"export LD_LIBRARY_PATH={CachePWQ_PATH}/simulator/noc/networking/booksim/native:$LD_LIBRARY_PATH\n"
            )

            cmd = [
                f"./{benchmark}",
                "-timing",
                "-no-progress-bar",
                "-report-all",
                "-scheduling round-robin",
                f"-platform-type {CONFIG}",
                "-mem-allocator-type interleaved",
            ]

            # benchmark-specific parameters
            if benchmark == "syrk":
                cmd.append("-max-inst 10000000")
            if benchmark == "syr2k":
                cmd.append("-max-inst 30000000")
            if benchmark == "convolution2d":
                cmd.append("-ni=8192 -nj=8192")
            if benchmark == "fastwalshtransform":
                cmd.append("-length=8388608")
            if benchmark == "jacobi1d":
                cmd.append("-n=67108864 -steps=1")
            if benchmark == "jacobi2d":
                cmd.append("-n=4096 -steps=1")
            if benchmark == "kmeans":
                cmd.append("-points=524288 -features=32 -clusters=20 -max-iter=1")
            if benchmark == "matrixtranspose":
                cmd.append("-width=2048")
            if benchmark == "mis":
                cmd.append("-numNodes=524288 -numItems=1048576")
            if benchmark == "pagerank":
                cmd.append("-node=8192 -sparsity=0.5 -iterations=1")
            if benchmark == "simpleconvolution":
                cmd.append("-width=8190 -height=8190")
            if benchmark == "shoc-reduction":
                cmd.append("-Size=67108864 -Iterations=2")
            if benchmark == "spmv":
                cmd.append("-dim=2097152 -sparsity=0.00001")
            if benchmark == "stencil2d":
                cmd.append("-row=2048 -col=2048")
            if benchmark == "syrk":
                cmd.append("-ni=2048 -nj=2048")
            if benchmark == "syr2k":
                cmd.append("-ni=1024 -nj=1024")

            # optional yaml config
            if yaml_path:
                cmd.append(f"-yaml-config-file {yaml_path}")

            if CONFIG == "monolithic":
                cmd.append(
                    f"-booksim-config-file {CachePWQ_PATH}/simulator/noc/networking/booksim/native/config_monolithic.icnt "
                )
            elif CONFIG == "SMSide":
                cmd.append(
                    f"-booksim-config-file {CachePWQ_PATH}/simulator/noc/networking/booksim/native/config_smside.icnt "
                )

            f.write(" ".join(cmd) + "\n")
            f.write(f'echo "[Done] {benchmark} finished."\n')

        os.chmod(file_path, 0o755)

    print(f"[OK] Generated run scripts and moved binaries to {base_output_dir}")


def generate_runners(yaml_path=None, eda_mode=False):
    timestamp = datetime.datetime.now().strftime("%Y-%m-%d_%H-%M-%S")
    base_output_dir = os.path.join("../runs", timestamp)
    os.makedirs(base_output_dir, exist_ok=True)

    for benchmark in BENCHMARKS:
        # --- Create benchmark-specific folder ---
        bench_dir = os.path.join(base_output_dir, benchmark)
        os.makedirs(bench_dir, exist_ok=True)

        # --- Move compiled binary ---
        src_bin = os.path.join(BENCH_PATH, benchmark, benchmark)
        dst_bin = os.path.join(bench_dir, benchmark)
        if os.path.exists(src_bin):
            shutil.move(src_bin, dst_bin)
        else:
            print(f"[WARN] No binary found for {benchmark}")

        # --- Generate runner script ---
        file_path = os.path.join(bench_dir, f"{benchmark}.sh")
        with open(file_path, "w") as f:
            if eda_mode:
                # EDA (bsub) header
                f.write("#!/bin/sh\n")
                f.write("#BSUB -q bmcpu\n")
                f.write(f"#BSUB -J {CONFIG}_{benchmark}\n")
                f.write("#BSUB -n 1\n")
                f.write(f"#BSUB -o {CONFIG}_{benchmark}.out\n")
                f.write(f"#BSUB -e {CONFIG}_{benchmark}.err\n")
                f.write("set -e\n")
            else:
                # Normal local execution
                f.write("#!/bin/bash\n")
                f.write("set -e\n")

            f.write(
                "export LD_LIBRARY_PATH=/hpc/home/connect.zchen097/CachePWQ/simulator/noc/networking/booksim/native:$LD_LIBRARY_PATH\n"
            )

            cmd = [
                f"./{benchmark}",
                "-timing",
                "-no-progress-bar",
                "-report-all",
                "-scheduling round-robin",
                f"-platform-type {CONFIG}",
                "-mem-allocator-type interleaved",
            ]

            # benchmark-specific parameters
            if benchmark == "syrk":
                cmd.append("-max-inst 10000000")
            if benchmark == "syr2k":
                cmd.append("-max-inst 30000000")
            if benchmark == "convolution2d":
                cmd.append("-ni=8192 -nj=8192")
            if benchmark == "fastwalshtransform":
                cmd.append("-length=8388608")
            if benchmark == "jacobi1d":
                cmd.append("-n=67108864 -steps=1")
            if benchmark == "jacobi2d":
                cmd.append("-n=4096 -steps=1")
            if benchmark == "kmeans":
                cmd.append("-points=524288 -features=32 -clusters=20 -max-iter=1")
            if benchmark == "matrixtranspose":
                cmd.append("-width=2048")
            if benchmark == "mis":
                cmd.append("-numNodes=524288 -numItems=1048576")
            if benchmark == "pagerank":
                cmd.append("-node=8192 -sparsity=0.5 -iterations=1")
            if benchmark == "simpleconvolution":
                cmd.append("-width=8190 -height=8190")
            if benchmark == "shoc-reduction":
                cmd.append("-Size=67108864 -Iterations=2")
            if benchmark == "spmv":
                cmd.append("-dim=2097152 -sparsity=0.00001")
            if benchmark == "stencil2d":
                cmd.append("-row=2048 -col=2048")
            if benchmark == "syrk":
                cmd.append("-ni=2048 -nj=2048")
            if benchmark == "syr2k":
                cmd.append("-ni=1024 -nj=1024")

            # optional yaml config
            if yaml_path:
                cmd.append(f"-yaml-config-file {yaml_path}")

            if CONFIG == "monolithic":
                cmd.append(
                    "-booksim-config-file /hpc/home/connect.zchen097/CachePWQ/simulator/noc/networking/booksim/native/config_monolithic.icnt "
                )
            elif CONFIG == "SMSide":
                cmd.append(
                    "-booksim-config-file /hpc/home/connect.zchen097/CachePWQ/simulator/noc/networking/booksim/native/config_smside.icnt "
                )

            f.write(" ".join(cmd) + "\n")
            f.write(f'echo "[Done] {benchmark} finished."\n')

        os.chmod(file_path, 0o755)

    print(f"[OK] Generated run scripts and moved binaries to {base_output_dir}")


# ==========================================================
# Main Entry
# ==========================================================


def main():
    parser = argparse.ArgumentParser(
        description="Compile benchmarks and generate run scripts."
    )
    parser.add_argument(
        "--use-yaml", type=str, help="Path to yaml config file", default=None
    )
    parser.add_argument(
        "--eda", action="store_true", help="Enable EDA (bsub) script mode"
    )
    args = parser.parse_args()

    compile_all()

    if args.eda:
        generate_runners(yaml_path=args.use_yaml, eda_mode=args.eda)
    else:
        generate_runners_on_desktop(yaml_path=args.use_yaml)


if __name__ == "__main__":
    main()
