import argparse
import os
import pandas as pd
import numpy as np
import matplotlib.pyplot as plt
from benchmark import get_benchmarks, get_short_name

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


def collect_data(benchmark_name: str, input_dir: str) -> tuple[float, float, float, float]:
    """
    Collects performance data from the specified input directory.

    Args:
        benchmark_name (str): The name of the benchmark.
        input_dir (str): The directory containing performance data files.

    Returns:
        list: A list of dictionaries containing performance data.
    """
    idle = 0
    base = 0
    translation = 0
    miss = 0

    file_path = os.path.join(input_dir, f"{benchmark_name}.csv")

    if not os.path.exists(file_path):
        print(f"Performance data file {file_path} does not exist.")

        return idle, base, translation, miss

    # read the CSV file and collect performance data
    df = pd.read_csv(file_path)

    for _, row in df.iterrows():
        if not "L1VCache" in row.iloc[1]:
            continue

        if row.iloc[2] == " idle":
            idle += row.iloc[3]
        elif row.iloc[2] == " base":
            base += row.iloc[3]
        elif row.iloc[2] == " translation":
            translation += row.iloc[3]
        elif row.iloc[2] == " miss":
            miss += row.iloc[3]

    total = idle + base + translation + miss

    return idle / total * 100, base / total * 100, translation / total * 100, miss / total * 100

def plot_PICS(
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
    r1 = np.arange(len(benchmarks)) * (1 * bar_width + 0.1)

    # # Ave.
    # data = pd.concat(
    #     [
    #         data,
    #         pd.DataFrame(
    #             {
    #                 "benchmark": ["Ave."],
    #                 "hits": [harmonic_mean(data["hits"])],
    #                 "misses": [harmonic_mean(data["misses"])],
    #                 "mshr_hits": [harmonic_mean(data["mshr_hits"])],
    #             }
    #         ),
    #     ],
    #     ignore_index=True,
    # )

    bar1 = plt.bar(
        r1,
        data["idle"],
        label="idle",
        width=bar_width,
        color="#fcfdf7",
        edgecolor="black",
        linewidth=1.5,
    )
    bar2 = plt.bar(
        r1,
        data["base"],
        width=bar_width,
        label="base",
        color="#cce5d8",
        edgecolor="black",
        linewidth=1.5,
        bottom=data["idle"],
    )
    bar3 = plt.bar(
        r1,
        data["translation"],
        width=bar_width,
        label="translation",
        color="#f7c6c6",
        edgecolor="black",
        linewidth=1.5,
        bottom=data["idle"] + data["base"],
    )
    bar4 = plt.bar(
        r1,
        data["miss"],
        width=bar_width,
        label="miss",
        color="#3f6b5c",
        edgecolor="black",
        linewidth=1.5,
        bottom=data["idle"] + data["base"] + data["translation"],
    )

    plt.xlim(min(r1) - bar_width, max(r1) + bar_width)
    plt.xticks(
        [r for r in r1],
        [get_short_name(benchmarks[i]) for i in range(len(benchmarks))],
        fontsize=22,
        fontweight="bold",
        rotation=90,
    )
    plt.ylabel("Percentage (%)", fontsize=24, fontweight="bold")
    plt.yticks(
        np.arange(0, 101, 20),
        fontsize=24,
        fontweight="bold",
    )
    plt.ylim(0, 100)
    plt.legend(
        loc="upper center",
        ncol=4,
        bbox_to_anchor=(0.5, 1),
        bbox_transform=plt.gcf().transFigure,  # 使用图形坐标系
        frameon=True,
        fancybox=True,
        framealpha=0.7,
        prop={"weight": "bold", "size": 20},
    )
    plt.tight_layout(rect=[0, 0, 1, 0.95])
    plt.grid(axis="y", alpha=0.3)

    ax = plt.gca()

    # 设置图的边框加粗
    for spine in ax.spines.values():
        spine.set_linewidth(1.75)  # 设置边框宽度为 2.5，可根据需要调整

    output_file = os.path.join(
        out_dir, "baseline_cache_utilization"
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

    data = pd.DataFrame(
        columns=["benchmark", "idle", "base", "translation", "miss"],
    )
    for benchmark in get_benchmarks():
        idle, base, translation, miss = collect_data(
            benchmark_name=benchmark,
            input_dir="../../data/baseline-l3tlb-monitor",
        )

        data = pd.concat(
            [
                data,
                pd.DataFrame(
                    {
                        "benchmark": [benchmark],
                        "idle": [idle],
                        "base": [base],
                        "translation": [translation],
                        "miss": [miss],
                    }
                ),
            ],
            ignore_index=True,
        )

    plot_PICS(
        data=data.copy(),
        out_dir=args.outDir,
    )
