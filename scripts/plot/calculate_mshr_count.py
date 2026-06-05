import argparse
import os
import numpy as np
import pandas as pd
import matplotlib.pyplot as plt

import benchmark


def harmonic_mean(df: pd.DataFrame) -> float:
    data = df.tolist()
    if not data or any(x <= 0 for x in data):
        return 0.0
    reciprocal_sum = sum(1 / x for x in data)
    return len(data) / reciprocal_sum

def collect_mshr_count(benchmark_name: str, input_dir: str) -> float:
    performance_data = 0
    
    file_path = os.path.join(input_dir, f"{benchmark_name}.csv")
    if not os.path.exists(file_path):
        print(f"Performance data file {file_path} does not exist.")
        return performance_data
    
    df = pd.read_csv(file_path)
    
    for _, row in df.iterrows():
        if "CaPWQL1VCache_01" in row.iloc[1]:
            if row.iloc[2] == " ptw-read-miss":
                performance_data += row.iloc[3]
            elif row.iloc[2] == " ptw-read-mshr-hit":
                performance_data += row.iloc[3]

    return performance_data

def collect_kernel_time(benchmark_name: str, input_dir: str) -> float:
    performance_data = 0
    
    file_path = os.path.join(input_dir, f"{benchmark_name}.csv")
    
    if not os.path.exists(file_path):
        print(f"Performance data file {file_path} does not exist.")
        return performance_data
    
    df = pd.read_csv(file_path)
    
    for _, row in df.iterrows():
        if row.iloc[1] == " driver" and row.iloc[2] == " kernel_time":
            performance_data = row.iloc[3]
            if performance_data == 0:
                print(f"Warning: kernel time for {benchmark_name} is zero.")
                continue
            else:
                break
        if (
            row.iloc[1] == " GPU1.CommandProcessor"
            and row.iloc[2] == " kernel_time (force stop) 0"
        ):
            performance_data = row.iloc[3]
            print(f"Warning: kernel time (force stop) for {benchmark_name} is {performance_data}.")
            break
    return performance_data

if __name__ == "__main__":
    parser = argparse.ArgumentParser(
        description="Parse MMU trace file and generate mmu shared report."
    )

    # Collect performance data for each benchmark
    data = pd.DataFrame(columns=["Benchmark", "Count", "Time"])
    
    for benchmark in benchmark.get_benchmarks():
        count = collect_mshr_count(benchmark, "/Users/chenzihang/Develop/CachePWQ/final_data/final_ngat_adaptive")
        time = collect_kernel_time(benchmark, "/Users/chenzihang/Develop/CachePWQ/final_data/final_ngat_adaptive") * 1e9
        
        if time == 0:
            print(f"Warning: Kernel time for {benchmark} is zero, skipping frequency calculation.")
            continue
        
        data = pd.concat(
            [
                data,
                pd.DataFrame(
                    {
                        "Benchmark": [benchmark],
                        "Count": [count],
                        "Time": [time],
                    }
                ),
            ],
            ignore_index=True,
        )
        
        print(f"Benchmark: {benchmark}, Count: {count}, Time: {time:.2f} cycle")
        
    frequency = data["Count"] / data["Time"]
    
    avg_frequency = harmonic_mean(frequency)
    print(f"Average MSHR Count Frequency: {avg_frequency:.2f} counts/cycle")
