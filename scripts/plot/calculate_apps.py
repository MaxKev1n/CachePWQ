import argparse
import os
import pandas as pd
import numpy as np
from benchmark import get_benchmarks, get_short_name

def collect_inst_count(
    benchmark_name: str,
    input_dir: str,
) -> float:
    """
    Collects performance data from the specified input directory.

    Args:
        benchmark_name (str): The name of the benchmark.
        input_dir (str): The directory containing performance data files.

    Returns:
        list: A list of dictionaries containing performance data.
    """
    performance_data = 0

    file_path = os.path.join(input_dir, f"{benchmark_name}.csv")

    if not os.path.exists(file_path):
        print(f"Performance data file {file_path} does not exist.")

        return performance_data

    # read the CSV file and collect performance data
    df = pd.read_csv(file_path)

    for _, row in df.iterrows():
        if row.iloc[2] == " inst_count":
            performance_data += row.iloc[3]

            if performance_data == 0:
                print(f"Warning: kernel time for {benchmark_name} is zero.")

                continue

    return performance_data

def collect_l2tlb_miss(
    benchmark_name: str,
    input_dir: str,
) -> float:
    """
    Collects performance data from the specified input directory.

    Args:
        benchmark_name (str): The name of the benchmark.
        input_dir (str): The directory containing performance data files.

    Returns:
        list: A list of dictionaries containing performance data.
    """
    performance_data = 0

    file_path = os.path.join(input_dir, f"{benchmark_name}.csv")

    if not os.path.exists(file_path):
        print(f"Performance data file {file_path} does not exist.")

        return performance_data

    # read the CSV file and collect performance data
    df = pd.read_csv(file_path)

    for _, row in df.iterrows():
        if row.iloc[1] == " GPU1.chiplet_00.L2TLB" and row.iloc[2] == " tlb-miss":
            performance_data += row.iloc[3]

            if performance_data == 0:
                print(f"Warning: kernel time for {benchmark_name} is zero.")

                continue

    return performance_data

if __name__ == "__main__":
    inst_count = pd.DataFrame(
        columns=["Benchmark", "Data"],
    )

    for benchmark in get_benchmarks():
        perf_data = collect_inst_count(
            benchmark_name=benchmark,
            input_dir="../../data/HierarchicalMemSide-monolithic-tlb",
        )

        inst_count = pd.concat(
            [
                inst_count,
                pd.DataFrame(
                    {
                        "Benchmark": [benchmark],
                        "Data": [perf_data],
                    }
                ),
            ],
            ignore_index=True,
        )

    print(inst_count)

    l2tlb_mpki = pd.DataFrame(
        columns=["Benchmark", "Data"],
    )

    for benchmark in get_benchmarks():
        perf_data = collect_l2tlb_miss(
            benchmark_name=benchmark,
            input_dir="../../data/HierarchicalMemSide-monolithic-tlb",
        )

        inst_count_data = inst_count[inst_count["Benchmark"] == benchmark]["Data"].values[0]

        l2tlb_mpki = pd.concat(
            [
                l2tlb_mpki,
                pd.DataFrame(
                    {
                        "Benchmark": [benchmark],
                        "Data": [perf_data / (inst_count_data / 1000)],
                    }
                ),
            ],
            ignore_index=True,
        )

    print(l2tlb_mpki.round(2))

    