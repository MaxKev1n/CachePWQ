#!/usr/bin/python3
import os
import subprocess
import argparse
from datetime import datetime
import sys
import time

memory_overhead = {
    "convolution2d": 33603,
    "fastwalshtransform": 12817,
    "gups": 1925,
    "jacobi1d": 43281,
    "jacobi2d": 47072,
    "kmeans": 28810,
    "matrixtranspose": 21837,
    "mis": 8281,
    "pagerank": 31996,
    "shoc-reduction": 36918,
    "simpleconvolution": 33928,
    "stencil2d": 45778,
    "syr2k": 52848,
    "syrk": 26594,
}


SIMULATOR_DIR = os.path.dirname(os.path.abspath(__file__)) + "/../simulator"
TOTAL_MEMORY_MB = 50 * 1024 # 50 GB
CHECK_INTERVAL = 15 * 60 # 15 minutes

def get_task_memory(benchmark_name):
    if benchmark_name in memory_overhead:
        print(f"[INFO] Memory overhead for {benchmark_name}: {memory_overhead[benchmark_name]}MB")

        return memory_overhead[benchmark_name]
    
    else:
        print(f"Memory overhead for benchmark '{benchmark_name}' not defined in memory_overhead dict.")

def run_with_scheduler(base_dir):
    base_dir = os.path.abspath(base_dir)
    pending_tasks = []
    
    for root, _, files in os.walk(base_dir):
        for file in files:
            if file.endswith(".sh"):
                name = os.path.splitext(file)[0]
                pending_tasks.append({
                    "name": name,
                    "path": os.path.join(root, file),
                    "dir": root,
                    "memory": get_task_memory(name)
                })

    pending_tasks.sort(key=lambda x: x["memory"])

    running_processes = []
    current_used_mem = 0
    completed_count = 0
    total_count = len(pending_tasks)

    print(f"[INFO] Total tasks found: {total_count}. Budget: {TOTAL_MEMORY_MB}MB")

    while pending_tasks or running_processes:
        still_running = []
        for item in running_processes:
            poll = item["proc"].poll()
            if poll is None:
                still_running.append(item)
            else:
                current_used_mem -= item["task"]["memory"]
                completed_count += 1
                status = "OK" if poll == 0 else f"FAILED({poll})"
                print(f"[{datetime.now().strftime('%H:%M:%S')}] [FINISHED] {item['task']['name']} with status {status}. "
                      f"Free memory: {TOTAL_MEMORY_MB - current_used_mem}MB")
        
        running_processes = still_running

        while pending_tasks:
            next_task = pending_tasks[0]
            if current_used_mem + next_task["memory"] <= TOTAL_MEMORY_MB:
                pending_tasks.pop(0)
                
                print(f"[{datetime.now().strftime('%H:%M:%S')}] [LAUNCHING] {next_task['name']} "
                      f"(Needs {next_task['memory']}MB).")
                
                out_f = open(os.path.join(next_task["dir"], f"{next_task['name']}.out"), "w")
                err_f = open(os.path.join(next_task["dir"], f"{next_task['name']}.err"), "w")
                
                proc = subprocess.Popen(
                    ["bash", next_task["path"]],
                    cwd=next_task["dir"],
                    stdout=out_f,
                    stderr=err_f
                )
                
                running_processes.append({"proc": proc, "task": next_task})
                current_used_mem += next_task["memory"]
            else:
                break

        if not pending_tasks and not running_processes:
            break

        if pending_tasks:
            print(f"[IDLE] Memory full ({current_used_mem}/{TOTAL_MEMORY_MB}). Waiting {CHECK_INTERVAL/60}min...")
            time.sleep(CHECK_INTERVAL)
        else:
            time.sleep(60)

    print(f"\n[DONE] All {total_count} tasks processed.")


def log_run_info(base_dir):
    """Record commit id, datetime, and executed command to a log file."""
    log_path = os.path.join(base_dir, "run_summary.log")

    with open(log_path, "a") as f:
        f.write("==== Execution Summary ====\n")

        # Current time
        now = datetime.now().strftime("%Y-%m-%d %H:%M:%S")
        f.write(f"Time: {now}\n")

        # Python command
        cmd_line = " ".join(sys.argv)
        f.write(f"Command: {cmd_line}\n")

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
    args = parser.parse_args()

    if not os.path.isdir(args.dir):
        print(f"[ERROR] Directory not found: {args.dir}")
        return

    run_with_scheduler(args.dir)
    log_run_info(args.dir)


if __name__ == "__main__":
    main()
