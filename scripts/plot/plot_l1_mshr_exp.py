import argparse
import os
import pandas as pd
import numpy as np
import matplotlib.pyplot as plt
from benchmark import get_high_mpki_benchmarks, get_short_name
from matplotlib.patches import Patch


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
    walker_unique_lens = 0
    unique_lens = 0

    file_path = os.path.join(input_dir, f"{benchmark_name}.csv")

    if not os.path.exists(file_path):
        print(f"Performance data file {file_path} does not exist.")

        return unique_lens, walker_unique_lens

    # read the CSV file and collect performance data
    df = pd.read_csv(file_path)

    for _, row in df.iterrows():
        if "L1VCache" not in row.iloc[1]:
            continue

        if row.iloc[2] == " average_mshr_uniq_len":
            unique_lens += row.iloc[3]
            count += 1

        elif row.iloc[2] == " average_walk_mshr_len":
            walker_unique_lens += row.iloc[3]

    return unique_lens / float(count), walker_unique_lens / float(count)


def plot_normalized_time(
    baseline: pd.DataFrame,
    Opt1: pd.DataFrame,
    Opt2: pd.DataFrame,
    Opt3: pd.DataFrame,
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
    plt.rcParams["hatch.linewidth"] = 2.0

    plt.figure(figsize=(20, 5), dpi=300)

    benchmarks = get_high_mpki_benchmarks()

    bar_width = 0.15
    r1 = np.arange(len(benchmarks) + 1) * (4 * bar_width + 0.2)
    r2 = [x + bar_width for x in r1]
    r3 = [x + bar_width for x in r2]
    r4 = [x + bar_width for x in r3]

    # Normalize the time
    # Opt1["MSHR"] = [
    #     (
    #         baseline["MSHR"][i] / Opt1["MSHR"][i]
    #         if Opt1["MSHR"][i] != 0 and baseline["MSHR"][i] != 0
    #         else 0
    #     )
    #     for i in range(len(benchmarks))
    # ]
    # Opt2["MSHR"] = [
    #     (
    #         baseline["MSHR"][i] / Opt2["MSHR"][i]
    #         if Opt2["MSHR"][i] != 0 and baseline["MSHR"][i] != 0
    #         else 0
    #     )
    #     for i in range(len(benchmarks))
    # ]
    # baseline["MSHR"] = [1.0 for _ in range(len(benchmarks))]

    # Ave.
    baseline = pd.concat(
        [
            baseline,
            pd.DataFrame(
                {
                    "Benchmark": ["Ave."],
                    "MSHR": [harmonic_mean(baseline["MSHR"])],
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
                    "Benchmark": ["Ave."],
                    "MSHR": [harmonic_mean(Opt1["MSHR"])],
                }
            ),
        ],
        ignore_index=True,
    )
    Opt2 = pd.concat(
        [
            Opt2,
            pd.DataFrame(
                {
                    "Benchmark": ["Ave."],
                    "MSHR": [harmonic_mean(Opt2["MSHR"])],
                }
            ),
        ],
        ignore_index=True,
    )
    Opt3 = pd.concat(
        [
            Opt3,
            pd.DataFrame(
                {
                    "Benchmark": ["Ave."],
                    "MSHR": [harmonic_mean(Opt3["MSHR"])],
                }
            ),
        ],
        ignore_index=True,
    )

    bar1 = plt.bar(
        r1,
        baseline["MSHR"],
        width=bar_width,
        label="Baseline",
        color="#8D2E2C",
        edgecolor="black",
        linewidth=1.5,
    )
    bar1_walker = plt.bar(
        r1,
        baseline["Walker_MSHR"],
        width=bar_width,
        color="#8D2E2C",
        edgecolor="black",
        linewidth=1.5,
        bottom=baseline["MSHR"],
        hatch="xx",
    )
    bar2 = plt.bar(
        r2,
        Opt1["MSHR"],
        width=bar_width,
        label="ngAT",
        color="#C3D9F1",
        edgecolor="black",
        linewidth=1.5,
    )
    bar2_walker = plt.bar(
        r2,
        Opt1["Walker_MSHR"],
        width=bar_width,
        color="#C3D9F1",
        edgecolor="black",
        linewidth=1.5,
        bottom=Opt1["MSHR"],
        hatch="xx",
    )
    bar3 = plt.bar(
        r3,
        Opt2["MSHR"],
        width=bar_width,
        label="ngAT + ARM",
        color="#5D73A1",
        edgecolor="black",
        linewidth=1.5,
    )
    bar3_walker = plt.bar(
        r3,
        Opt2["Walker_MSHR"],
        width=bar_width,
        color="#5D73A1",
        edgecolor="black",
        linewidth=1.5,
        bottom=Opt2["MSHR"],
        hatch="xx",
    )
    bar4 = plt.bar(
        r4,
        Opt3["MSHR"],
        width=bar_width,
        label="Infinite Walker",
        color="#313A5B",
        edgecolor="black",
        linewidth=1.5,
    )
    bar4_walker = plt.bar(
        r4,
        Opt3["Walker_MSHR"],
        width=bar_width,
        color="#313A5B",
        edgecolor="black",
        linewidth=1.5,
        bottom=Opt3["MSHR"],
        hatch="xx",
    )

    for bar in bar1 + bar2 + bar3 + bar4:
        height = bar.get_height()
        x = bar.get_x() + bar.get_width() / 2

        # if height >= 4:
        #     plt.annotate(
        #         f"{height:.2f}",
        #         xy=(x, 3.65),
        #         xytext=(0, 0),  # 相对偏移 (0,15) 表示向上15pt
        #         textcoords="offset points",
        #         ha="center",
        #         va="bottom",
        #         fontsize=22,
        #         fontweight="bold",
        #         bbox=dict(
        #             facecolor="white",
        #             edgecolor="black",
        #             boxstyle="round,pad=0.1",
        #         ),
        #         # arrowprops=dict(arrowstyle="-", color="red", lw=2),
        #     )
        # else:
        #     plt.annotate(
        #         f"{height:.2f}",
        #         xy=(x, height),
        #         xytext=(0, 0),  # 相对偏移 (0,15) 表示向上15pt
        #         textcoords="offset points",
        #         ha="center",
        #         va="bottom",
        #         fontsize=22,
        #         fontweight="bold",
        #         rotation=90,
        #         # bbox=dict(
        #         #     facecolor="white",
        #         #     edgecolor="black",
        #         #     boxstyle="round,pad=0.1",
        #         # ),
        #         # arrowprops=dict(arrowstyle="-", color="red", lw=2),
        #     )

    plt.xlim(min(r1) - bar_width, max(r4) + bar_width)
    plt.xticks(
        [r + 1.5 * bar_width for r in r1],
        [get_short_name(benchmarks[i]) for i in range(len(benchmarks))] + ["Ave."],
        fontsize=28,
        fontweight="bold",
    )
    plt.ylabel("Average L1\n MSHR Occupancy", fontsize=28, fontweight="bold")
    plt.yticks(
        np.arange(0, 32, 8),
        fontsize=28,
        fontweight="bold",
    )
    plt.ylim(0, 32)

    hatch_patch = Patch(
        facecolor="white",
        edgecolor="black",
        hatch="xx",
        label="ngAT MSHR",
    )

    plt.legend(
        handles=[bar1, bar2, bar3, bar4, hatch_patch],  # 加入 hatch_patch
        loc="upper center",
        ncol=5,  # 从 4 改为 5
        bbox_to_anchor=(0.5, 1),
        bbox_transform=plt.gcf().transFigure,
        frameon=True,
        fancybox=True,
        framealpha=0.7,
        prop={"weight": "bold", "size": 26},
    )
    plt.tight_layout(rect=[0, 0, 1, 0.95])
    plt.grid(axis="y", alpha=0.3)
    # plt.axhline(y=8, color="red", linewidth=0.8, linestyle="--")

    ax = plt.gca()

    # 设置图的边框加粗
    for spine in ax.spines.values():
        spine.set_linewidth(1.75)  # 设置边框宽度为 2.5，可根据需要调整

    output_file = os.path.join(
        out_dir, "CaPWQMMU_L1_MSHR_Occupancy"
    )
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
        columns=["Benchmark", "MSHR", "Walker_MSHR"],
    )
    Opt1 = pd.DataFrame(
        columns=["Benchmark", "MSHR", "Walker_MSHR"],
    )
    Opt2 = pd.DataFrame(
        columns=["Benchmark", "MSHR", "Walker_MSHR"],
    )
    Opt3 = pd.DataFrame(
        columns=["Benchmark", "MSHR", "Walker_MSHR"],
    )

    for benchmark in get_high_mpki_benchmarks():
        mshr, walker_mshr = collect_mshr(
            benchmark_name=benchmark,
            input_dir="../../final_data/final_baseline",
        )

        baseline = pd.concat(
            [
                baseline,
                pd.DataFrame(
                    {
                        "Benchmark": [benchmark],
                        "MSHR": [mshr],
                        "Walker_MSHR": [walker_mshr],
                    }
                ),
            ],
            ignore_index=True,
        )

        mshr, walker_mshr = collect_mshr(
            benchmark_name=benchmark,
            input_dir="../../final_data/final_ngat_0",
        )

        Opt1 = pd.concat(
            [
                Opt1,
                pd.DataFrame(
                    {
                        "Benchmark": [benchmark],
                        "MSHR": [mshr],
                        "Walker_MSHR": [walker_mshr],
                    }
                ),
            ],
            ignore_index=True,
        )

        mshr, walker_mshr = collect_mshr(
            benchmark_name=benchmark,
            input_dir="../../final_data/final_ngat_adaptive",
        )

        Opt2 = pd.concat(
            [
                Opt2,
                pd.DataFrame(
                    {
                        "Benchmark": [benchmark],
                        "MSHR": [mshr],
                        "Walker_MSHR": [walker_mshr],
                    }
                ),
            ],
            ignore_index=True,
        )
        
        mshr, walker_mshr = collect_mshr(
            benchmark_name=benchmark,
            input_dir="../../final_data/final_infinitewalker",
        )

        Opt3 = pd.concat(
            [
                Opt3,
                pd.DataFrame(
                    {
                        "Benchmark": [benchmark],
                        "MSHR": [mshr],
                        "Walker_MSHR": [walker_mshr],
                    }
                ),
            ],
            ignore_index=True,
        )

    plot_normalized_time(
        baseline=baseline.copy(),
        Opt1=Opt1.copy(),
        Opt2=Opt2.copy(),
        Opt3=Opt3.copy(),
        out_dir=args.outDir,
    )
