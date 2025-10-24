#!/usr/bin/python3
import os
import subprocess
import argparse


def run_all_scripts(base_dir, eda_mode=False):
    for root, dirs, files in os.walk(base_dir):
        for file in files:
            if file.endswith(".sh"):
                script_path = os.path.join(root, file)
                benchmark_name = os.path.splitext(file)[0]
                print(f"[Running] {benchmark_name} ...")

                if eda_mode:
                    cmd = f"bsub < {script_path}"
                else:
                    out_file = os.path.join(root, f"{benchmark_name}.out")
                    err_file = os.path.join(root, f"{benchmark_name}.err")
                    cmd = f"bash {script_path} > {out_file} 2> {err_file} &"

                ret = subprocess.call(cmd, shell=True)
                if ret != 0:
                    print(f"[ERROR] {benchmark_name} failed (exit code {ret})")
                else:
                    print(f"[OK] {benchmark_name} submitted/executed successfully.")

    print(f"[DONE] All scripts executed from {base_dir}")


def main():
    parser = argparse.ArgumentParser(description="Run or submit benchmark scripts.")
    parser.add_argument(
        "--dir",
        type=str,
        required=True,
        help="Path to the run directory (e.g., ../runs/2025-10-24_10-24-53)",
    )
    parser.add_argument(
        "--eda",
        action="store_true",
        help="If set, use EDA (bsub) mode; otherwise run locally",
    )
    args = parser.parse_args()

    if not os.path.isdir(args.dir):
        print(f"[ERROR] Directory not found: {args.dir}")
        return

    run_all_scripts(args.dir, eda_mode=args.eda)


if __name__ == "__main__":
    main()
