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


def collect_mshr(benchmark_name: str, input_dir: str) -> tuple[float, float]:
    """
    Collects performance data from the specified input directory.

    Args:
        benchmark_name (str): The name of the benchmark.
        input_dir (str): The directory containing performance data files.

    Returns:
        list: A list of dictionaries containing performance data.
    """
    count = 0
    lens = 0
    unique_lens = 0

    file_path = os.path.join(input_dir, f"{benchmark_name}.csv")

    if not os.path.exists(file_path):
        print(f"Performance data file {file_path} does not exist.")

        return lens, unique_lens

    # read the CSV file and collect performance data
    df = pd.read_csv(file_path)

    for _, row in df.iterrows():
        if "L3TLB0" not in row.iloc[1]:
            continue

        if row.iloc[2] == " average_mshr_uniq_len_g0":
            unique_lens += row.iloc[3]
            count += 1

        elif row.iloc[2] == " average_mshr_len_g0":
            lens += row.iloc[3]

    return lens / float(count), unique_lens / float(count)

def plot_mshr_size(
    baseline: pd.DataFrame,
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

    plt.figure(figsize=(8, 5), dpi=300)

    benchmarks = get_benchmarks()

    bar_width = 0.15
    r1 = np.arange(len(benchmarks) + 1) * (1 * bar_width + 0.1)

    # Ave.
    baseline = pd.concat(
        [
            baseline,
            pd.DataFrame(
                {
                    "Benchmark": ["Ave."],
                    "MSHR_Size": [harmonic_mean(baseline["MSHR_Size"])],
                    "MSHR_Len": [harmonic_mean(baseline["MSHR_Len"])],
                }
            ),
        ],
        ignore_index=True,
    )

    bar1 = plt.bar(
        r1,
        baseline["MSHR_Size"],
        width=bar_width,
        color="#fcfdf7",
        edgecolor="black",
        linewidth=1.5,
    )
    # bar2 = plt.bar(
    #     r2,
    #     Opt1["Data"],
    #     width=bar_width,
    #     label="MemSide CapWQ 5 Cycle Latency NoC",
    #     color="#cce5d8",
    #     edgecolor="black",
    #     linewidth=1.5,
    # )

    for bar in bar1:
        height = bar.get_height()
        x = bar.get_x() + bar.get_width() / 2

        if height >= 512:
            plt.annotate(
            f"{height:.1f}",
            xy=(x, 500),
            xytext=(0, 10),  # 相对偏移 (0,15) 表示向上15pt
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

    plt.xlim(min(r1) - bar_width, max(r1) + bar_width)
    plt.xticks(
        [r for r in r1],
        [get_short_name(benchmarks[i]) for i in range(len(benchmarks))] + ["Ave."],
        fontsize=22,
        fontweight="bold",
        rotation=90,
    )
    plt.ylabel("Average MSHR Occupancy", fontsize=24, fontweight="bold")
    plt.yticks(
        np.arange(0, 512.1, 128),
        fontsize=22,
        fontweight="bold",
    )
    plt.ylim(0, 512)
    # plt.legend(
    #     loc="upper center",
    #     ncol=1,
    #     bbox_to_anchor=(0.5, 1),
    #     bbox_transform=plt.gcf().transFigure,  # 使用图形坐标系
    #     frameon=True,
    #     fancybox=True,
    #     framealpha=0.7,
    #     prop={"weight": "bold", "size": 20},
    # )
    plt.tight_layout(rect=[0, 0, 1, 0.95])
    plt.grid(axis="y", alpha=0.3)

    ax = plt.gca()

    # 设置图的边框加粗
    for spine in ax.spines.values():
        spine.set_linewidth(1.75)  # 设置边框宽度为 2.5，可根据需要调整

    output_file = os.path.join(
        out_dir, "L3TLB_MSHR_Size"
    )
    plt.savefig(output_file + ".png", dpi=300)
    plt.savefig(output_file + ".pdf", dpi=300)
    print(f"Plot saved to {output_file}")

def plot_mshr_len(
    baseline: pd.DataFrame,
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

    plt.figure(figsize=(8, 5), dpi=300)

    benchmarks = get_benchmarks()

    bar_width = 0.15
    r1 = np.arange(len(benchmarks) + 1) * (1 * bar_width + 0.1)

    # Ave.
    baseline = pd.concat(
        [
            baseline,
            pd.DataFrame(
                {
                    "Benchmark": ["Ave."],
                    "MSHR_Size": [harmonic_mean(baseline["MSHR_Size"])],
                    "MSHR_Len": [harmonic_mean(baseline["MSHR_Len"])],
                }
            ),
        ],
        ignore_index=True,
    )

    bar1 = plt.bar(
        r1,
        baseline["MSHR_Len"],
        width=bar_width,
        color="#cce5d8",
        edgecolor="black",
        linewidth=1.5,
    )
    # bar2 = plt.bar(
    #     r2,
    #     Opt1["Data"],
    #     width=bar_width,
    #     label="MemSide CapWQ 5 Cycle Latency NoC",
    #     color="#cce5d8",
    #     edgecolor="black",
    #     linewidth=1.5,
    # )

    for bar in bar1:
        height = bar.get_height()
        x = bar.get_x() + bar.get_width() / 2

        if height >= 2.5:
            plt.annotate(
            f"{height:.1f}",
            xy=(x, 2.2),
            xytext=(0, 5),  # 相对偏移 (0,15) 表示向上15pt
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

    plt.xlim(min(r1) - bar_width, max(r1) + bar_width)
    plt.xticks(
        [r for r in r1],
        [get_short_name(benchmarks[i]) for i in range(len(benchmarks))] + ["Ave."],
        fontsize=22,
        fontweight="bold",
        rotation=90,
    )
    plt.ylabel("Average Slot Length", fontsize=24, fontweight="bold")
    plt.yticks(
        np.arange(0, 2.6, 0.5),
        fontsize=22,
        fontweight="bold",
    )
    plt.ylim(0, 2.5)
    # plt.legend(
    #     loc="upper center",
    #     ncol=1,
    #     bbox_to_anchor=(0.5, 1),
    #     bbox_transform=plt.gcf().transFigure,  # 使用图形坐标系
    #     frameon=True,
    #     fancybox=True,
    #     framealpha=0.7,
    #     prop={"weight": "bold", "size": 20},
    # )
    plt.tight_layout(rect=[0, 0, 1, 0.95])
    plt.grid(axis="y", alpha=0.3)

    ax = plt.gca()

    # 设置图的边框加粗
    for spine in ax.spines.values():
        spine.set_linewidth(1.75)  # 设置边框宽度为 2.5，可根据需要调整

    output_file = os.path.join(
        out_dir, "L3TLB_MSHR_Len"
    )
    plt.savefig(output_file + ".png", dpi=300)
    plt.savefig(output_file + ".pdf", dpi=300)
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
        columns=["Benchmark", "MSHR_Size", "MSHR_Len"],
    )

    for benchmark in get_benchmarks():
        lens, unique_lens = collect_mshr(
            benchmark_name=benchmark,
            input_dir="../../data/baseline-numa-latency",
        )

        if lens == 0 and unique_lens == 0:
            baseline = pd.concat(
                [
                    baseline,
                    pd.DataFrame(
                        {
                            "Benchmark": [benchmark],
                            "MSHR_Size": [0],
                            "MSHR_Len": [0]
                        }
                    ),
                ],
                ignore_index=True,
            )
        else:
            baseline = pd.concat(
                [
                    baseline,
                    pd.DataFrame(
                        {
                            "Benchmark": [benchmark],
                            "MSHR_Size": [lens],
                            "MSHR_Len": [lens/unique_lens]
                        }
                    ),
                ],
                ignore_index=True,
            )

    plot_mshr_size(
        baseline=baseline.copy(),
        out_dir=args.outDir,
    )
    plot_mshr_len(
        baseline=baseline.copy(),
        out_dir=args.outDir,
    )
