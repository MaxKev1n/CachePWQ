import argparse
import os
import pandas as pd
import numpy as np
import matplotlib.pyplot as plt
from benchmark import get_benchmarks, get_short_name


def geometric_mean(df: pd.DataFrame) -> float:
    """
    Calculate the geometric mean of a list of numbers.

    Args:
        data (list): A list of numerical values.

    Returns:
        float: The geometric mean of the input data.
    """
    data = df.tolist()
    if not data or any(x <= 0 for x in data):
        return 0.0

    product = np.prod(data)
    return product ** (1 / len(data))


def collect_performance_csv(
    file_path: str,
) -> float:
    """
    Collects performance data from the specified input directory.

    Args:
        benchmark_name (str): The name of the benchmark.
        input_dir (str): The directory containing performance data files.

    Returns:
        list: A list of dictionaries containing performance data.
    """
    performance_data = [0, 0, 0]

    if not os.path.exists(file_path):
        print(f"Performance data file {file_path} does not exist.")

        return performance_data

    # read the CSV file and collect performance data
    df = pd.read_csv(file_path)

    for _, row in df.iterrows():
        where = row.iloc[1]
        what = row.iloc[2]
        value = row.iloc[3]

        if "L2TLB" in where:
            if "-tlb-hit" in what:
                performance_data[0] += value
            elif "-tlb-miss" in what:
                performance_data[1] += value
            elif "-tlb-mshr-hit" in what:
                performance_data[2] += value

    return performance_data


def collect_performance_data(
    benchmark_name: str,
    demand_where: str,
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
    performance_data = [0, 0, 0]

    file_path = os.path.join(input_dir, f"{benchmark_name}.csv")

    if not os.path.exists(file_path):
        print(f"Performance data file {file_path} does not exist.")

        return performance_data

    # read the CSV file and collect performance data
    df = pd.read_csv(file_path)

    for _, row in df.iterrows():
        where = row.iloc[1]
        what = row.iloc[2]
        value = row.iloc[3]

        if demand_where in where:
            if "-mshr-hit" in what:
                performance_data[2] += value
            elif "-miss" in what:
                performance_data[1] += value
            elif "-hit" in what:
                performance_data[0] += value

    return performance_data


def plot_miss_rate(
    l1tlb: pd.DataFrame,
    l2tlb: pd.DataFrame,
    l1cache: pd.DataFrame,
    l2cache: pd.DataFrame,
    out_dir: str,
) -> None:
    """
    Plots the normalized time for private and shared data.

    Args:
        result (pd.DataFrame): DataFrame containing performance data.
        baseline (pd.DataFrame): DataFrame containing baseline performance data.
        out_dir (str): Directory to save the output plots.
    """
    if not os.path.exists(out_dir):
        os.makedirs(out_dir)

    # Set Arial font family
    plt.rcParams["font.family"] = "Arial"
    # For macOS, you might need to explicitly set the font file
    plt.rcParams["font.sans-serif"] = ["Arial"]
    plt.rcParams["mathtext.fontset"] = "custom"
    plt.rcParams["mathtext.rm"] = "Arial"
    plt.rcParams["mathtext.it"] = "Arial:italic"
    plt.rcParams["mathtext.bf"] = "Arial:bold"

    plt.figure(figsize=(20, 5), dpi=300)

    benchmarks = get_benchmarks()

    bar_width = 0.2
    r1 = np.arange(len(benchmarks)) * (4 * bar_width + 0.2)
    r2 = [x + bar_width for x in r1]
    r3 = [x + bar_width for x in r2]
    r4 = [x + bar_width for x in r3]

    L1VTLB_miss_rate = l1tlb["Miss"] / (
        l1tlb["Hit"] + l1tlb["Miss"] + l1tlb["MSHR-Hit"]
    )
    L2TLB_miss_rate = l2tlb["Miss"] / (l2tlb["Hit"] + l2tlb["Miss"] + l2tlb["MSHR-Hit"])
    L1VCache_miss_rate = l1cache["Miss"] / (
        l1cache["Hit"] + l1cache["Miss"] + l1cache["MSHR-Hit"]
    )
    L2Cache_miss_rate = l2cache["Miss"] / (
        l2cache["Hit"] + l2cache["Miss"] + l2cache["MSHR-Hit"]
    )

    bar1 = plt.bar(
        r1,
        L1VTLB_miss_rate,
        width=bar_width,
        label="L1VTLB",
        color="#fcfdf7",
        edgecolor="black",
        linewidth=1.5,
    )
    bar2 = plt.bar(
        r2,
        L2TLB_miss_rate,
        width=bar_width,
        label="L2TLB",
        color="#cce5d8",
        edgecolor="black",
        linewidth=1.5,
    )
    bar3 = plt.bar(
        r3,
        L1VCache_miss_rate,
        width=bar_width,
        label="L1VCache",
        color="#6ba78b",
        edgecolor="black",
        linewidth=1.5,
    )
    bar4 = plt.bar(
        r4,
        L2Cache_miss_rate,
        width=bar_width,
        label="L2Cache",
        color="#3f6b5c",
        edgecolor="black",
        linewidth=1.5,
    )

    plt.xlim(min(r1) - bar_width, max(r4) + bar_width)
    plt.xticks(
        [r + 1.5 * bar_width for r in r1],
        [get_short_name(benchmarks[i]) for i in range(len(benchmarks))],
        fontsize=22,
        fontweight="bold",
    )
    plt.ylabel("Miss Rate", fontsize=22, fontweight="bold")
    plt.yticks(
        np.arange(0, 1.1, 0.25),
        fontsize=22,
        fontweight="bold",
    )
    plt.ylim(0, 1)
    plt.legend(
        loc="upper center",
        ncol=5,
        bbox_to_anchor=(0.5, 1),
        bbox_transform=plt.gcf().transFigure,  # 使用图形坐标系
        frameon=True,
        fancybox=True,
        framealpha=0.7,
        prop={"weight": "bold", "size": 22},
    )
    plt.tight_layout(rect=[0, 0, 1, 0.95])
    plt.grid(axis="y", alpha=0.3)
    plt.axhline(y=1, color="red", linewidth=0.8, linestyle="--")

    ax = plt.gca()

    # 设置图的边框加粗
    for spine in ax.spines.values():
        spine.set_linewidth(1.75)  # 设置边框宽度为 2.5，可根据需要调整

    output_file = os.path.join(out_dir, "MemorySide_Miss_Rate")
    plt.savefig(output_file + ".png")
    plt.savefig(output_file + ".pdf")
    print(f"Plot saved to {output_file}")


def single_benchmark(args):
    perf_data = collect_performance_csv(
        file_path=args.csv,
    )
    print(perf_data)
    print(
        perf_data,
        (perf_data[0] + perf_data[2]) / (perf_data[0] + perf_data[1] + perf_data[2]),
        sum(perf_data),
    )


def multi_benchmark(args):
    L1VTLB = pd.DataFrame(
        columns=["Benchmark", "Hit", "Miss", "MSHR-Hit"],
    )
    L2TLB = pd.DataFrame(
        columns=["Benchmark", "Hit", "Miss", "MSHR-Hit"],
    )
    L1VCache = pd.DataFrame(
        columns=["Benchmark", "Hit", "Miss", "MSHR-Hit"],
    )
    L2Cache = pd.DataFrame(
        columns=["Benchmark", "Hit", "Miss", "MSHR-Hit"],
    )

    for benchmark in get_benchmarks():
        perf_data = collect_performance_data(
            benchmark_name=benchmark,
            demand_where="L1VTLB",
            input_dir="../../data/baseline",
        )
        L1VTLB = pd.concat(
            [
                L1VTLB,
                pd.DataFrame(
                    {
                        "Benchmark": [benchmark],
                        "Hit": [perf_data[0]],
                        "Miss": [perf_data[1]],
                        "MSHR-Hit": [perf_data[2]],
                    }
                ),
            ],
            ignore_index=True,
        )

        perf_data = collect_performance_data(
            benchmark_name=benchmark,
            demand_where="L2TLB",
            input_dir="../../data/baseline",
        )
        L2TLB = pd.concat(
            [
                L2TLB,
                pd.DataFrame(
                    {
                        "Benchmark": [benchmark],
                        "Hit": [perf_data[0]],
                        "Miss": [perf_data[1]],
                        "MSHR-Hit": [perf_data[2]],
                    }
                ),
            ],
            ignore_index=True,
        )

        perf_data = collect_performance_data(
            benchmark_name=benchmark,
            demand_where="L1VCache",
            input_dir="../../data/baseline",
        )
        L1VCache = pd.concat(
            [
                L1VCache,
                pd.DataFrame(
                    {
                        "Benchmark": [benchmark],
                        "Hit": [perf_data[0]],
                        "Miss": [perf_data[1]],
                        "MSHR-Hit": [perf_data[2]],
                    }
                ),
            ],
            ignore_index=True,
        )

        perf_data = collect_performance_data(
            benchmark_name=benchmark,
            demand_where="L2_",
            input_dir="../../data/baseline",
        )
        L2Cache = pd.concat(
            [
                L2Cache,
                pd.DataFrame(
                    {
                        "Benchmark": [benchmark],
                        "Hit": [perf_data[0]],
                        "Miss": [perf_data[1]],
                        "MSHR-Hit": [perf_data[2]],
                    }
                ),
            ],
            ignore_index=True,
        )

    plot_miss_rate(
        L1VTLB,
        L2TLB,
        L1VCache,
        L2Cache,
        args.outDir,
    )


if __name__ == "__main__":
    # Example usage
    parser = argparse.ArgumentParser(description="Parse csv file.")

    parser.add_argument(
        "--outDir",
        required=True,
        type=str,
        help="Directory path to save the output plots.",
    )
    parser.add_argument(
        "--mode",
        required=True,
        type=str,
        choices=["single", "multi"],
        help="Benchmark mode: single or multi.",
    )
    parser.add_argument(
        "--csv",
        type=str,
        help="Path to the CSV file for single benchmark mode.",
    )

    args = parser.parse_args()

    if args.mode == "single":
        single_benchmark(args)
    else:
        multi_benchmark(args)
