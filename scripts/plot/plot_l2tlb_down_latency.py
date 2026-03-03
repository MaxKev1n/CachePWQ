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

def harmonic_mean(df: pd.DataFrame) -> float:
    """
    Calculate the harmonic mean of a list of numbers.

    Args:
        data (list): A list of numerical values.

    Returns:
        float: The harmonic mean of the input data.
    """
    data = df.tolist()
    if not data or any(x <= 0 for x in data):
        return 0.0

    reciprocal_sum = sum(1 / x for x in data)
    return len(data) / reciprocal_sum


def collect_performance_data(
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
    performance_data = [0, 0]

    file_path = os.path.join(input_dir, f"{benchmark_name}.csv")

    if not os.path.exists(file_path):
        print(f"Performance data file {file_path} does not exist.")

        return performance_data

    # read the CSV file and collect performance data
    df = pd.read_csv(file_path)

    for _, row in df.iterrows():
        if "GPU1.chiplet_00.L2TLB" in row.iloc[1] and row.iloc[2] == " down_req_average_latency":
            performance_data[0] = row.iloc[3] * 1e9

    return performance_data


def plot_normalized_latency(
    baseline: pd.DataFrame,
    Opt1: pd.DataFrame,
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

    bar_width = 0.4
    r1 = np.arange(len(benchmarks) + 1) * (2 * bar_width + 0.4)
    r2 = [x + bar_width for x in r1]

    # Geometric mean
    baseline = pd.concat(
        [
            baseline,
            pd.DataFrame(
                {
                    "Benchmark": ["Geometric Mean"],
                    "Latency": [harmonic_mean(baseline["Latency"])],
                }
            ),
        ],
        ignore_index=True,
    )
    Opt1 = pd.concat(
        [
            Opt1,
            pd.DataFrame(
                {
                    "Benchmark": ["Geometric Mean"],
                    "Latency": [harmonic_mean(Opt1["Latency"])],
                }
            ),
        ],
        ignore_index=True,
    )

    bar1 = plt.bar(
        r1,
        baseline["Latency"],
        width=bar_width,
        label="Baseline",
        color="#fcfdf7",
        edgecolor="black",
        linewidth=1.5,
    )
    bar2 = plt.bar(
        r2,
        Opt1["Latency"],
        width=bar_width,
        label="Low Latency NoC",
        color="#cce5d8",
        edgecolor="black",
        linewidth=1.5,
    )

    for bar in bar1 + bar2:
        height = bar.get_height()
        x = bar.get_x() + bar.get_width() / 2

        if height > 300:
            if bar in bar1:
                plt.annotate(
                f"{height:.1f}",
                xy=(x, 270),
                xytext=(0, 0),  # 相对偏移 (0,15) 表示向上15pt
                textcoords="offset points",
                ha="center",
                va="bottom",
                fontsize=20,
                fontweight="bold",
                bbox=dict(
                    facecolor="white",
                    edgecolor="black",
                    boxstyle="round,pad=0.1",
                ),
                # arrowprops=dict(arrowstyle="-", color="red", lw=2),
            )
            else:
                plt.annotate(
                f"{height:.1f}",
                xy=(x, 240),
                xytext=(0, 0),  # 相对偏移 (0,15) 表示向上15pt
                textcoords="offset points",
                ha="center",
                va="bottom",
                fontsize=20,
                fontweight="bold",
                bbox=dict(
                    facecolor="white",
                    edgecolor="black",
                    boxstyle="round,pad=0.1",
                ),
                # arrowprops=dict(arrowstyle="-", color="red", lw=2),
            )

    plt.xlim(min(r1) - bar_width, max(r2) + bar_width)
    plt.xticks(
        [r + 0.5 * bar_width for r in r1],
        [get_short_name(benchmarks[i]) for i in range(len(benchmarks))] + ["GMean"],
        fontsize=22,
        fontweight="bold",
    )
    plt.ylabel("Normalized NoC Latency", fontsize=22, fontweight="bold")
    plt.yticks(
        np.arange(0, 301, 60),
        fontsize=22,
        fontweight="bold",
    )
    plt.ylim(0, 300)
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

    output_file = os.path.join(out_dir, "NoC_Latency_Comparison")
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

    args = parser.parse_args()

    baseline = pd.DataFrame(
        columns=["Benchmark", "Latency", "Count"],
    )
    Opt1 = pd.DataFrame(
        columns=["Benchmark", "Latency", "Count"],
    )

    for benchmark in get_benchmarks():
        perf_data = collect_performance_data(
            benchmark_name=benchmark,
            input_dir="../../data/HierarchicalMemorySideBaseline",
        )

        baseline = pd.concat(
            [
                baseline,
                pd.DataFrame(
                    {
                        "Benchmark": [benchmark],
                        "Latency": [perf_data[0]],
                    }
                ),
            ],
            ignore_index=True,
        )

        perf_data = collect_performance_data(
            benchmark_name=benchmark,
            input_dir="../../data/HierarchicalMemorySideLowLatency",
        )

        Opt1 = pd.concat(
            [
                Opt1,
                pd.DataFrame(
                    {
                        "Benchmark": [benchmark],
                        "Latency": [perf_data[0]],
                    }
                ),
            ],
            ignore_index=True,
        )

    plot_normalized_latency(
        baseline=baseline.copy(),
        Opt1=Opt1.copy(),
        out_dir=args.outDir,
    )
