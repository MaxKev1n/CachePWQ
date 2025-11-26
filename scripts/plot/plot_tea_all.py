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
    performance_data = [0, 0, 0, 0, 0]

    if not os.path.exists(file_path):
        print(f"Performance data file {file_path} does not exist.")

        return performance_data

    # read the file in line and collect performance data
    with open(file_path, "r") as f:
        for line in f:
            if line.startswith("GPU core:"):
                continue

            if "Dumping TEA logs..." in line:
                continue

            line = line.strip()
            values = line.split(", ")

            event = int(values[1])
            value = int(values[2])

            performance_data[event] += float(value)

    return performance_data


def plot_tea(
    data: pd.DataFrame,
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
    r1 = np.arange(len(benchmarks)) * (bar_width + 0.2)

    Base = data["Base"].tolist()
    L1TLBMISS = data["L1TLBMiss"].tolist()
    L2TLBMISS = data["L2TLBMiss"].tolist()
    L1CACHEMISS = data["L1CacheMiss"].tolist()
    L2CACHEMISS = data["L2CacheMiss"].tolist()

    Total = (
        np.array(Base)
        + np.array(L1TLBMISS)
        + np.array(L2TLBMISS)
        + np.array(L1CACHEMISS)
        + np.array(L2CACHEMISS)
    )

    Base_Percent = np.array(Base) / Total * 100
    L1TLBMISS_Percent = np.array(L1TLBMISS) / Total * 100
    L2TLBMISS_Percent = np.array(L2TLBMISS) / Total * 100
    L1CACHEMISS_Percent = np.array(L1CACHEMISS) / Total * 100
    L2CACHEMISS_Percent = np.array(L2CACHEMISS) / Total * 100

    bar1 = plt.bar(
        r1,
        Base_Percent,
        width=bar_width,
        label="Base",
        color="#fcfdf7",
        edgecolor="black",
        linewidth=1.5,
    )
    bar2 = plt.bar(
        r1,
        L1TLBMISS_Percent,
        width=bar_width,
        label="L1TLBMiss",
        bottom=Base_Percent,
        color="#cce5d8",
        edgecolor="black",
        linewidth=1.5,
    )
    bar3 = plt.bar(
        r1,
        L2TLBMISS_Percent,
        width=bar_width,
        label="L2TLBMiss",
        bottom=Base_Percent + L1TLBMISS_Percent,
        color="#6ba78b",
        edgecolor="black",
        linewidth=1.5,
    )
    bar4 = plt.bar(
        r1,
        L1CACHEMISS_Percent,
        width=bar_width,
        label="L1CacheMiss",
        bottom=Base_Percent + L1TLBMISS_Percent + L2TLBMISS_Percent,
        color="#3f6b5c",
        edgecolor="black",
        linewidth=1.5,
    )
    bar5 = plt.bar(
        r1,
        L2CACHEMISS_Percent,
        width=bar_width,
        label="L2CacheMiss",
        bottom=Base_Percent
        + L1TLBMISS_Percent
        + L2TLBMISS_Percent
        + L1CACHEMISS_Percent,
        color="#1f4036",
        edgecolor="black",
        linewidth=1.5,
    )

    plt.xticks(
        [r for r in r1],
        [get_short_name(benchmarks[i]) for i in range(len(benchmarks))],
        fontsize=22,
        fontweight="bold",
    )
    plt.ylabel("% of Events Cycles", fontsize=22, fontweight="bold")
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
    plt.tight_layout(rect=[0, 0, 1, 0.9])
    plt.grid(axis="y", alpha=0.3)

    ax = plt.gca()

    # 设置图的边框加粗
    for spine in ax.spines.values():
        spine.set_linewidth(1.75)  # 设置边框宽度为 2.5，可根据需要调整

    output_file = os.path.join(out_dir, "32Walkers_TEA")
    plt.savefig(output_file + ".png")
    plt.savefig(output_file + ".pdf")
    print(f"Plot saved to {output_file}")


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
        "--inputDir",
        type=str,
        help="Directory path to load the traces.",
    )

    args = parser.parse_args()

    data = pd.DataFrame(
        columns=[
            "Benchmark",
            "Base",
            "L1TLBMiss",
            "L2TLBMiss",
            "L1CacheMiss",
            "L2CacheMiss",
        ],
    )

    for benchmark in get_benchmarks():
        perf_data = collect_performance(
            file_path=args.inputDir + "/" + benchmark + ".tea.trace",
        )

        data = pd.concat(
            [
                data,
                pd.DataFrame(
                    {
                        "Benchmark": [benchmark],
                        "Base": [perf_data[0]],
                        "L1TLBMiss": [perf_data[1]],
                        "L2TLBMiss": [perf_data[2]],
                        "L1CacheMiss": [perf_data[3]],
                        "L2CacheMiss": [perf_data[4]],
                    }
                ),
            ],
            ignore_index=True,
        )

    plot_tea(
        data=data,
        out_dir=args.outDir,
    )
