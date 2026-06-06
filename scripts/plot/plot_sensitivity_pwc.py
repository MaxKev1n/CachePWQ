import argparse
import os
import pandas as pd
import numpy as np
import matplotlib.pyplot as plt
from benchmark import get_high_mpki_benchmarks, get_short_name
from matplotlib.patches import Patch

def harmonic_mean(df: pd.DataFrame) -> float:
    data = df.tolist()
    if not data or any(x <= 0 for x in data):
        return 0.0
    reciprocal_sum = sum(1 / x for x in data)
    return len(data) / reciprocal_sum


def collect_performance_data(benchmark_name: str, input_dir: str) -> float:
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


def plot_normalized_time(
    baseline_128: pd.DataFrame,
    nbwalker_128: pd.DataFrame,
    baseline_32: pd.DataFrame,
    nbwalker_32: pd.DataFrame,
    baseline_512: pd.DataFrame,
    nbwalker_512: pd.DataFrame,
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

    bar_width = 0.1
    r1 = np.arange(len(benchmarks) + 1) * (3 * bar_width + 0.2)
    r2 = [x + bar_width for x in r1]
    r3 = [x + bar_width for x in r2]

    # Normalize the time
    nbwalker_32["Data"] = [
        (
            baseline_32["Data"][i] / nbwalker_32["Data"][i]
            if nbwalker_32["Data"][i] != 0 and baseline_32["Data"][i] != 0
            else 0
        )
        for i in range(len(benchmarks))
    ]
    nbwalker_128["Data"] = [
        (
            baseline_128["Data"][i] / nbwalker_128["Data"][i]
            if nbwalker_128["Data"][i] != 0 and baseline_128["Data"][i] != 0
            else 0
        )
        for i in range(len(benchmarks))
    ]
    nbwalker_512["Data"] = [
        (
            baseline_512["Data"][i] / nbwalker_512["Data"][i]
            if nbwalker_512["Data"][i] != 0 and baseline_512["Data"][i] != 0
            else 0
        )
        for i in range(len(benchmarks))
    ]

    # Ave.
    nbwalker_32 = pd.concat(
        [
            nbwalker_32,
            pd.DataFrame(
                {
                    "Benchmark": ["Ave."],
                    "Data": [harmonic_mean(nbwalker_32["Data"])],
                }
            ),
        ],
        ignore_index=True,
    )
    nbwalker_128 = pd.concat(
        [
            nbwalker_128,
            pd.DataFrame(
                {
                    "Benchmark": ["Ave."],
                    "Data": [harmonic_mean(nbwalker_128["Data"])],
                }
            ),
        ],
        ignore_index=True,
    )
    nbwalker_512 = pd.concat(
        [
            nbwalker_512,
            pd.DataFrame(
                {
                    "Benchmark": ["Ave."],
                    "Data": [harmonic_mean(nbwalker_512["Data"])],
                }
            ),
        ],
        ignore_index=True,
    )
    
    bar1 = plt.bar(
        r1,
        nbwalker_32["Data"],
        width=bar_width,
        label=r"32 Entries (0.25$\times$)",
        color="#C3D9F1",
        edgecolor="black",
        linewidth=1.5,
    )
    bar2 = plt.bar(
        r2,
        nbwalker_128["Data"],
        width=bar_width,
        label=r"128 Entries (1$\times$)",
        color="#5D73A1",
        edgecolor="black",
        linewidth=1.5,
    )
    bar3 = plt.bar(
        r3,
        nbwalker_512["Data"],
        width=bar_width,
        label=r"512 Entries (4$\times$)",
        color="#313A5B",
        edgecolor="black",
        linewidth=1.5,
    )

    for bar in bar1 + bar2 + bar3:
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

    plt.xlim(min(r1) - bar_width, max(r3) + bar_width)
    plt.xticks(
        [r + 1 * bar_width for r in r1],
        [get_short_name(benchmarks[i]) for i in range(len(benchmarks))] + ["HMean"],
        fontsize=36,
        fontweight="bold",
    )
    plt.ylabel("Speedup", fontsize=36, fontweight="bold")
    plt.yticks(
        np.arange(0, 6.1, 2),
        fontsize=36,
        fontweight="bold",
    )
    plt.ylim(0, 6)
    
    plt.legend(
        loc="upper center",
        ncol=4,
        bbox_to_anchor=(0.5, 1),
        bbox_transform=plt.gcf().transFigure,
        frameon=True,
        fancybox=True,
        framealpha=0.7,
        prop={"weight": "bold", "size": 28},
    )
    
    plt.tight_layout(rect=[0, 0, 1, 0.925])
    plt.grid(axis="y", alpha=0.3)
    plt.axhline(y=1, color="red", linewidth=0.8, linestyle="--")

    ax = plt.gca()

    # 设置图的边框加粗
    for spine in ax.spines.values():
        spine.set_linewidth(1.75)  # 设置边框宽度为 2.5，可根据需要调整

    output_file = os.path.join(
        out_dir, "sensitivity_pwc"
    )
    plt.savefig(output_file + ".png")
    plt.savefig(output_file + ".pdf")
    print(f"Plot saved to {output_file}")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="Parse csv file.")
    parser.add_argument("--outDir", required=True, type=str,
                        help="Directory path to save the output plots.")
    args = parser.parse_args()

    # Cleaner loop — rebuild from scratch properly
    baseline_32 = pd.DataFrame(columns=["Benchmark", "Data"])
    nbwalker_32     = pd.DataFrame(columns=["Benchmark", "Data"])
    baseline_128 = pd.DataFrame(columns=["Benchmark", "Data"])
    nbwalker_128     = pd.DataFrame(columns=["Benchmark", "Data"])
    baseline_512 = pd.DataFrame(columns=["Benchmark", "Data"])
    nbwalker_512     = pd.DataFrame(columns=["Benchmark", "Data"])

    for benchmark in get_high_mpki_benchmarks():
        def append_row(df, benchmark, input_dir):
            perf_data = collect_performance_data(benchmark_name=benchmark, input_dir=input_dir)
            return pd.concat(
                [df, pd.DataFrame({"Benchmark": [benchmark], "Data": [perf_data]})],
                ignore_index=True,
            )

        baseline_32 = append_row(baseline_32, benchmark, "../../final_final_data/baseline_32PWCEntries")
        nbwalker_32     = append_row(nbwalker_32,     benchmark, "../../final_final_data/nbwalker_32PWCEntries")
        baseline_128 = append_row(baseline_128, benchmark, "../../final_final_data/baseline")
        nbwalker_128     = append_row(nbwalker_128,     benchmark, "../../final_final_data/nbwalker-full")
        baseline_512 = append_row(baseline_512,     benchmark, "../../final_final_data/baseline_512PWCEntries")
        nbwalker_512     = append_row(nbwalker_512,     benchmark, "../../final_final_data/nbwalker_512PWCEntries")

    plot_normalized_time(
        baseline_128=baseline_128.copy(),
        nbwalker_128=nbwalker_128.copy(),
        baseline_32=baseline_32.copy(),
        nbwalker_32=nbwalker_32.copy(),
        baseline_512=baseline_512.copy(),
        nbwalker_512=nbwalker_512.copy(),
        out_dir=args.outDir,
    )