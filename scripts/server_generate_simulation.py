#!/usr/bin/python3
import os
import subprocess
import argparse
import datetime
import shutil
import sys

from plot.benchmark import memory_overhead

# ==========================================================
# Configuration
# ==========================================================

CachePWQ_PATH = os.environ["CAPWQ_ROOT"]

if not CachePWQ_PATH:
    print("[ERROR] Please set the environment variable CAPWQ_ROOT to the root directory of CachePWQ.")
    sys.exit(1)

MGPU_PATH = f"{CachePWQ_PATH}/simulator/mgpusim/"
BENCH_PATH = os.path.join(MGPU_PATH, "samples")
CONFIG = "numa"
SIZE = "normal"  # normal or large

BENCHMARKS = [
    "gups",
    "kmeans",
    "matrixtranspose",
    "pagerank",
    "spmv",
    "stencil2d",
    "gesummv",
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

def round_memory(benchmark_name: str) -> int:
    if benchmark_name in memory_overhead:
        print(f"[INFO] Memory overhead for {benchmark_name}: {memory_overhead[benchmark_name]}MB")

        if memory_overhead[benchmark_name] <= 2048:
            return 2048
        elif memory_overhead[benchmark_name] <= 4096:
            return 4096
        elif memory_overhead[benchmark_name] <= 8192:
            return 8192
        elif memory_overhead[benchmark_name] <= 16384:
            return 16384
        elif memory_overhead[benchmark_name] <= 32768:
            return 32768
        elif memory_overhead[benchmark_name] <= 49152:
            return 49152
        elif memory_overhead[benchmark_name] <= 65536:
            return 65536
        else:
            return memory_overhead[benchmark_name]

    else:
        assert False, f"Memory overhead for benchmark '{benchmark_name}' not defined in memory_overhead dict."

def generate_runners(yaml_path=None, large_size=False):
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
            shutil.copy(src_bin, dst_bin)
        else:
            print(f"[WARN] No binary found for {benchmark}")

        # --- Generate runner script ---
        file_path = os.path.join(bench_dir, f"{benchmark}.sh")
        with open(file_path, "w") as f:
            f.write("#!/bin/sh\n")
            f.write("#SBATCH -p i64m512ue\n")
            f.write("#SBATCH -n 1\n")
            f.write(f"#SBATCH -J {CONFIG}_{benchmark}\n")
            f.write(f"#SBATCH -o {CONFIG}_{benchmark}.out\n")
            f.write(f"#SBATCH -e {CONFIG}_{benchmark}.err\n")
            f.write(f"#SBATCH --mem {str(round_memory(benchmark))}\n")
            f.write("set -e\n")
                
            f.write(
                f"export LD_LIBRARY_PATH={CachePWQ_PATH}/simulator/libs:$LD_LIBRARY_PATH\n"
            )

            if benchmark == "gups":
                f.write(f"cp {CachePWQ_PATH}/simulator/mgpusim/samples/gups/starts.bin ./starts.bin\n")

            cmd = [
                f"./{benchmark}",
                "-timing",
                "-no-progress-bar",
                "-report-all",
                "-scheduling round-robin",
                f"-platform-type {CONFIG}",
                "-mem-allocator-type demandpaging",
                "-use-unified-memory",
                # "-capwq-monitor",
            ]

            # benchmark-specific parameters
            if large_size:
                if benchmark == "kmeans": # 8.25 GB
                    cmd.append("-points=2097152 -features=16 -clusters=20 -max-iter=1 ")
                if benchmark == "matrixtranspose": # 8 GB
                    cmd.append("-width=8192 ")
                if benchmark == "pagerank": # 6.25 GB
                    cmd.append("-node=16384 -sparsity=0.5 -iterations=1 ")
                if benchmark == "spmv": # 4 GB
                    cmd.append("-dim=2097152 -sparsity=0.00001 ")
                if benchmark == "stencil2d": # 8 GB
                    cmd.append("-row=8192 -col=8192 ")
                if benchmark == "gesummv":  # 8 GB
                    cmd.append("-n=8192 ")

            else: # 4 GB
                if benchmark == "kmeans": # 8.25 GB
                    cmd.append("-points=67108864 -features=16 -clusters=20 -max-iter=1 ")
                if benchmark == "matrixtranspose": # 8 GB
                    cmd.append("-width=32768 ")
                if benchmark == "pagerank": # 6.25 GB
                    cmd.append("-node=40960 -sparsity=0.5 -iterations=1 ")
                if benchmark == "spmv": # 4 GB
                    cmd.append("-dim=7200000 -sparsity=0.00001 ")
                if benchmark == "stencil2d": # 8 GB
                    cmd.append("-row=32768 -col=32768 ")
                if benchmark == "gesummv":  # 8 GB
                    cmd.append("-n=32768 ")

            if benchmark == "syrk":
                cmd.append("-max-inst 10000000 ")
            if benchmark == "syr2k":
                cmd.append("-max-inst 30000000 ")
            if benchmark == "gups":
                cmd.append("-max-inst 10000000 ")
            if benchmark == "gesummv":
                cmd.append("-max-inst 2000000 ")


            # optional yaml config
            if yaml_path:
                cmd.append(f"-yaml-config-file {yaml_path}")

            if CONFIG == "numa":
                cmd.append(
                    f"-global-noc-config-file {CachePWQ_PATH}/simulator/noc/networking/booksim/native/config_numa.icnt "
                )
                cmd.append(
                    f"-booksim-dir {CachePWQ_PATH}/simulator/noc/networking/booksim/native/ "
                )
            else:
                assert 0, "Unsupported CONFIG for desktop mode"

            f.write(" ".join(cmd) + "\n")
            f.write(f'echo "[Done] {benchmark} finished."\n')

        os.chmod(file_path, 0o755)

    print(f"[OK] Generated run scripts and moved binaries to {base_output_dir}")

    return base_output_dir

def generate_runners_on_desktop(yaml_path=None, large_size=False):
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
            # Normal local execution
            f.write("#!/bin/bash\n")
            f.write("set -e\n")

            f.write(
                f"export LD_LIBRARY_PATH={CachePWQ_PATH}/simulator/libs:$LD_LIBRARY_PATH\n"
            )

            cmd = [
                f"./{benchmark}",
                "-timing",
                "-no-progress-bar",
                "-report-all",
                "-scheduling round-robin",
                f"-platform-type {CONFIG}",
                "-mem-allocator-type pta",
            ]

            # benchmark-specific parameters
            if large_size:
                if benchmark == "atax":
                    cmd.append("-x=8192 -y=8192 ")  # 128 MB
                if benchmark == "bicg":
                    cmd.append("-x=8192 -y=8192 ")  # 256 MB
                if benchmark == "convolution2d":
                    cmd.append("-ni=16384 -nj=16384 ")  # 2048 MB
                if benchmark == "fastwalshtransform":
                    cmd.append("-length=67108864 ")  # 256 MB
                if benchmark == "jacobi1d":  # 2048 MB
                    cmd.append("-n=268435456 -steps=1")
                if benchmark == "jacobi2d":  # 2048 MB
                    cmd.append("-n=16384 -steps=1")
                if benchmark == "kmeans":  # 1024 MB
                    cmd.append(
                        "-points=4194304 -features=32 -clusters=20 -max-iter=1 "
                    )
                if benchmark == "matrixtranspose":  # 512 MB
                    cmd.append("-width=8192 ")
                if benchmark == "mis":  # 32 MB
                    cmd.append("-numNodes=1048576 -numItems=2097152 ")
                if benchmark == "pagerank":  # 1024 MB
                    cmd.append("-node=16384 -sparsity=0.5 -iterations=1 ")
                if benchmark == "simpleconvolution":  # 2048 MB
                    cmd.append("-width=16382 -height=16382 ")
                if benchmark == "shoc-reduction":  # 2048 MB
                    cmd.append("-Size=268435456 -Iterations=2 ")
                if benchmark == "spmv":  # 1390 MB
                    cmd.append("-dim=2097152 -sparsity=0.00001 ")
                if benchmark == "stencil2d":  # 512 MB
                    cmd.append("-row=8192 -col=8192 ")
                if benchmark == "syrk":  # 512 MB
                    cmd.append("-ni=8192 -nj=8192 ")
                if benchmark == "syr2k":  # 192 MB
                    cmd.append("-ni=4096 -nj=4096 ")

            else:
                if benchmark == "atax":
                    cmd.append("-x=4096 -y=4096 ")  # 64 MB
                if benchmark == "bicg":
                   cmd.append("-x=4096 -y=4096 ")  # 64 MB
                if benchmark == "convolution2d":
                    cmd.append("-ni=8192 -nj=8192 ")
                if benchmark == "fastwalshtransform":
                    cmd.append("-length=8388608 ")
                if benchmark == "jacobi1d":
                    cmd.append("-n=67108864 -steps=1 ")
                if benchmark == "jacobi2d":
                    cmd.append("-n=4096 -steps=1 ")
                if benchmark == "kmeans":
                    cmd.append("-points=524288 -features=32 -clusters=20 -max-iter=1 ")
                if benchmark == "matrixtranspose":
                    cmd.append("-width=2048 ")
                if benchmark == "mis":
                    cmd.append("-numNodes=524288 -numItems=1048576 ")
                if benchmark == "pagerank":
                    cmd.append("-node=8192 -sparsity=0.5 -iterations=1 ")
                if benchmark == "simpleconvolution":
                    cmd.append("-width=8190 -height=8190 ")
                if benchmark == "shoc-reduction":
                    cmd.append("-Size=67108864 -Iterations=2 ")
                if benchmark == "spmv":
                    cmd.append("-dim=2097152 -sparsity=0.00001 ")
                if benchmark == "stencil2d":
                    cmd.append("-row=2048 -col=2048 ")
                if benchmark == "syrk":
                    cmd.append("-ni=2048 -nj=2048 ")
                if benchmark == "syr2k":
                    cmd.append("-ni=1024 -nj=1024 ")

            if benchmark == "syrk":
                cmd.append("-max-inst 10000000 ")
            if benchmark == "syr2k":
                cmd.append("-max-inst 30000000 ")


            # optional yaml config
            if yaml_path:
                cmd.append(f"-yaml-config-file {yaml_path}")

            if CONFIG == "numa":
                cmd.append(
                    f"-global-noc-config-file {CachePWQ_PATH}/simulator/noc/networking/booksim/native/config_numa.icnt "
                )
                cmd.append(
                    f"-booksim-dir {CachePWQ_PATH}/simulator/noc/networking/booksim/native/ "
                )
                cmd.append("-capwq-monitor ")
            else:
                assert 0, "Unsupported CONFIG for desktop mode"

            f.write(" ".join(cmd) + "\n")
            f.write(f'echo "[Done] {benchmark} finished."\n')

        os.chmod(file_path, 0o755)

    print(f"[OK] Generated run scripts and moved binaries to {base_output_dir}")

    return base_output_dir

def log_run_info(base_dir, yaml_path):
    """Record commit id, datetime, and executed command to a log file."""
    log_path = os.path.join(base_dir, "run_summary.log")

    sim_dir = os.path.join(CachePWQ_PATH, "simulator")

    with open(log_path, "a") as f:
        f.write("==== Generate Summary ====\n")

        # Current time
        timestamp = datetime.datetime.now().strftime("%Y-%m-%d_%H-%M-%S")
        f.write(f"Time: {timestamp}\n")

        # Git commit id
        try:
            commit_id = (
                subprocess.check_output(["git", "rev-parse", "HEAD~3"])
                .decode("utf-8")
                .strip()
            )
        except subprocess.CalledProcessError:
            commit_id = "N/A (not a git repository)"
        f.write(f"Commit ID: {commit_id}\n")

        # Python command
        cmd_line = " ".join(sys.argv)
        f.write(f"Command: {cmd_line}\n")

        if os.path.isdir(sim_dir):
            try:
                diff_data = subprocess.check_output(
                    ["git", "diff", "HEAD", "."], cwd=sim_dir
                ).decode("utf-8").strip()

                if diff_data:
                    f.write("Status: DIRTY (Uncommitted changes found)\n")
                    f.write("-" * 10 + " GIT DIFF " + "-" * 10 + "\n")
                    f.write(diff_data + "\n")
                    f.write("-" * 30 + "\n")
                else:
                    f.write("Status: CLEAN (No uncommitted changes)\n")

            except subprocess.CalledProcessError:
                f.write("Git Info: Error retrieving git status (Is it a git repo?)\n")
        else:
            f.write(f"Git Info: SIMULATOR_DIR not found at {sim_dir}\n")

        f.write("\n")
    print(f"[INFO] Run info written to {log_path}")

    with open(log_path, "a") as f:
        f.write("==== Configurations ====\n")

        if yaml_path and os.path.exists(yaml_path):
            f.write(f"Source YAML File: {yaml_path}\n")
            f.write("-" * 10 + " YAML CONTENT " + "-" * 10 + "\n")
            try:
                with open(yaml_path, 'r') as yaml_file:
                    f.write(yaml_file.read())
            except Exception as e:
                f.write(f"Error reading YAML file: {e}\n")
            f.write("\n" + "-" * 30 + "\n")
        else:
            f.write("YAML Config: None (Using default parameters)\n")
        
        f.write("\n")
    print(f"[INFO] Configurations info written to {log_path}")   
        

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
    parser.add_argument(
        "--large-size", action="store_true", help="Enable large size workloads"
    )
    args = parser.parse_args()

    clean_all()
    compile_all()

    out_dir = ""

    if args.eda:
        out_dir = generate_runners(yaml_path=args.use_yaml, large_size=args.large_size)
    else:
        out_dir = generate_runners_on_desktop(yaml_path=args.use_yaml, large_size=args.large_size)

    log_run_info(out_dir, args.use_yaml)


if __name__ == "__main__":
    main()
