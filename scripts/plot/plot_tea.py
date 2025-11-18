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


def collect_performance(
    file_path: str,
) -> dict:
    """
    Collects performance data from the specified input directory.

    Args:
        benchmark_name (str): The name of the benchmark.
        input_dir (str): The directory containing performance data files.

    Returns:
        list: A list of dictionaries containing performance data.
    """
    performance_data = {}

    if not os.path.exists(file_path):
        print(f"Performance data file {file_path} does not exist.")

        return performance_data

    # read the file in line and collect performance data
    with open(file_path, "r") as f:
        for line in f:
            if line.startswith("GPU core:"):
                continue

            line = line.strip()
            values = line.split(", ")

            key = values[0]
            event = int(values[1])
            value = int(values[2])

            if key not in performance_data:
                performance_data[key] = [
                    0,
                    0,
                    0,
                    0,
                    0,
                ]

            performance_data[key][event] += float(value)

    return performance_data


def plot_tea(
    benchmark_name: str,
    data: set,
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

    bar_width = 0.4
    r1 = np.arange(len(data)) * (bar_width + 0.2)

    PCs = []
    Base = []
    L1TLBMISS = []
    L2TLBMISS = []
    L1CACHEMISS = []
    L2CACHEMISS = []

    Total = []

    for pc in data.keys():
        PCs.append(pc)

    for events in data.values():
        Base.append(events[0])
        L1TLBMISS.append(events[1])
        L2TLBMISS.append(events[2])
        L1CACHEMISS.append(events[3])
        L2CACHEMISS.append(events[4])
        Total.append(sum(events))

    # sort by Total in descending order
    sorted_indices = np.argsort(Total)[::-1]
    PCs = [PCs[i] for i in sorted_indices]
    Base = [Base[i] for i in sorted_indices]
    L1TLBMISS = [L1TLBMISS[i] for i in sorted_indices]
    L2TLBMISS = [L2TLBMISS[i] for i in sorted_indices]
    L1CACHEMISS = [L1CACHEMISS[i] for i in sorted_indices]
    L2CACHEMISS = [L2CACHEMISS[i] for i in sorted_indices]

    bar1 = plt.bar(
        r1,
        Base,
        width=bar_width,
        label="Base",
        color="#fcfdf7",
        edgecolor="black",
        linewidth=1.5,
    )
    bar2 = plt.bar(
        r1,
        L1TLBMISS,
        width=bar_width,
        label="L1TLBMiss",
        bottom=Base,
        color="#cce5d8",
        edgecolor="black",
        linewidth=1.5,
    )
    bar3 = plt.bar(
        r1,
        L2TLBMISS,
        width=bar_width,
        label="L2TLBMiss",
        bottom=np.array(Base) + np.array(L1TLBMISS),
        color="#6ba78b",
        edgecolor="black",
        linewidth=1.5,
    )
    bar4 = plt.bar(
        r1,
        L1CACHEMISS,
        width=bar_width,
        label="L1CacheMiss",
        bottom=np.array(Base) + np.array(L1TLBMISS) + np.array(L2TLBMISS),
        color="#3f6b5c",
        edgecolor="black",
        linewidth=1.5,
    )
    bar5 = plt.bar(
        r1,
        L2CACHEMISS,
        width=bar_width,
        label="L2CacheMiss",
        bottom=np.array(Base)
        + np.array(L1TLBMISS)
        + np.array(L2TLBMISS)
        + np.array(L1CACHEMISS),
        color="#1f4036",
        edgecolor="black",
        linewidth=1.5,
    )

    plt.xticks(
        [r for r in r1],
        PCs,
        fontsize=22,
        fontweight="bold",
        rotation=90,
    )
    plt.ylabel("Events Cycles", fontsize=22, fontweight="bold")
    plt.yticks(
        fontsize=22,
        fontweight="bold",
    )
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
    plt.title(
        f"TEA Result: Baseline L2 TLB - {benchmark_name}",
        fontsize=24,
        fontweight="bold",
    )
    plt.tight_layout(rect=[0, 0, 1, 0.9])
    plt.grid(axis="y", alpha=0.3)
    plt.axhline(y=1, color="red", linewidth=0.8, linestyle="--")

    ax = plt.gca()

    # 设置图的边框加粗
    for spine in ax.spines.values():
        spine.set_linewidth(1.75)  # 设置边框宽度为 2.5，可根据需要调整

    output_file = os.path.join(out_dir, f"{benchmark_name}_TEA")
    plt.savefig(output_file + ".png")
    plt.savefig(output_file + ".pdf")
    print(f"Plot saved to {output_file}")


def single_benchmark(args):
    perf_data = collect_performance(
        file_path=args.trace,
    )

    plot_tea(
        benchmark_name="MatrixTranspose",
        data=perf_data,
        out_dir=args.outDir,
    )


def multi_benchmark(args):
    assert False, "Not implemented yet."


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
        "--trace",
        type=str,
        help="Path to the tea trace file for single benchmark mode.",
    )

    args = parser.parse_args()

    if args.mode == "single":
        single_benchmark(args)
    else:
        multi_benchmark(args)
