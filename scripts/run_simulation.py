#!/usr/bin/python3
import os
import subprocess
import argparse
from datetime import datetime
import sys

SIMULATOR_DIR = os.path.dirname(os.path.abspath(__file__)) + "/../simulator"

def run_all_scripts(base_dir, eda_mode=False):
    base_dir = os.path.abspath(base_dir)
    cwd = os.getcwd()

    for root, dirs, files in os.walk(base_dir):
        for file in files:
            if file.endswith(".sh"):
                script_path = os.path.join(root, file)
                benchmark_name = os.path.splitext(file)[0]

                print(f"[Running] {benchmark_name} ...")

                os.chdir(root)

                if eda_mode:
                    cmd = f"bsub < {file}"
                else:
                    out_file = f"{benchmark_name}.out"
                    err_file = f"{benchmark_name}.err"
                    cmd = f"bash {file} > {out_file} 2> {err_file} &"

                ret = subprocess.call(cmd, shell=True)

                os.chdir(cwd)

                if ret != 0:
                    print(f"[ERROR] {benchmark_name} failed (exit code {ret})")
                else:
                    print(f"[OK] {benchmark_name} submitted/executed successfully.\n")

    print(f"[DONE] All scripts executed from {base_dir}")


def log_run_info(base_dir):
    """Record commit id, datetime, and executed command to a log file."""
    log_path = os.path.join(base_dir, "run_summary.log")

    sim_dir = os.path.abspath(SIMULATOR_DIR)

    with open(log_path, "a") as f:
        f.write("==== Run Summary ====\n")

        # Current time
        now = datetime.now().strftime("%Y-%m-%d %H:%M:%S")
        f.write(f"Time: {now}\n")

        # Git commit id
        try:
            commit_id = (
                subprocess.check_output(["git", "rev-parse", "HEAD"])
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
    log_run_info(args.dir)


if __name__ == "__main__":
    main()
