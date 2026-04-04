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
    baseline_32: pd.DataFrame,
    ngat_32: pd.DataFrame,
    baseline_48: pd.DataFrame,
    ngat_48: pd.DataFrame,
    baseline_64: pd.DataFrame,
    ngat_64: pd.DataFrame,
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
    r1 = np.arange(len(benchmarks) + 1) * (6 * bar_width + 0.2)
    r2 = [x + bar_width for x in r1]
    r3 = [x + bar_width for x in r2]
    r4 = [x + bar_width for x in r3]
    r5 = [x + bar_width for x in r4]
    r6 = [x + bar_width for x in r5]

    # Normalize the time
    ngat_32["Data"] = [
        (
            baseline_32["Data"][i] / ngat_32["Data"][i]
            if ngat_32["Data"][i] != 0 and baseline_32["Data"][i] != 0
            else 0
        )
        for i in range(len(benchmarks))
    ]
    ngat_48["Data"] = [
        (
            baseline_32["Data"][i] / ngat_48["Data"][i]
            if ngat_48["Data"][i] != 0 and baseline_32["Data"][i] != 0
            else 0
        )
        for i in range(len(benchmarks))
    ]
    ngat_64["Data"] = [
        (
            baseline_32["Data"][i] / ngat_64["Data"][i]
            if ngat_64["Data"][i] != 0 and baseline_32["Data"][i] != 0
            else 0
        )
        for i in range(len(benchmarks))
    ]
    baseline_48["Data"] = [
        (
            baseline_32["Data"][i] / baseline_48["Data"][i]
            if baseline_48["Data"][i] != 0 and baseline_32["Data"][i] != 0
            else 0
        )
        for i in range(len(benchmarks))
    ]
    baseline_64["Data"] = [
        (
            baseline_32["Data"][i] / baseline_64["Data"][i]
            if baseline_64["Data"][i] != 0 and baseline_32["Data"][i] != 0
            else 0
        )
        for i in range(len(benchmarks))
    ]
    baseline_32["Data"] = [1.0 for _ in range(len(benchmarks))]

    # Ave.
    baseline_32 = pd.concat(
        [
            baseline_32,
            pd.DataFrame(
                {
                    "Benchmark": ["Ave."],
                    "Data": [harmonic_mean(baseline_32["Data"])],
                }
            ),
        ],
        ignore_index=True,
    )
    ngat_32 = pd.concat(
        [
            ngat_32,
            pd.DataFrame(
                {
                    "Benchmark": ["Ave."],
                    "Data": [harmonic_mean(ngat_32["Data"])],
                }
            ),
        ],
        ignore_index=True,
    )
    baseline_48 = pd.concat(
        [
            baseline_48,
            pd.DataFrame(
                {
                    "Benchmark": ["Ave."],
                    "Data": [harmonic_mean(baseline_48["Data"])],
                }
            ),
        ],
        ignore_index=True,
    )
    ngat_48 = pd.concat(
        [
            ngat_48,
            pd.DataFrame(
                {
                    "Benchmark": ["Ave."],
                    "Data": [harmonic_mean(ngat_48["Data"])],
                }
            ),
        ],
        ignore_index=True,
    )
    baseline_64 = pd.concat(
        [
            baseline_64,
            pd.DataFrame(
                {
                    "Benchmark": ["Ave."],
                    "Data": [harmonic_mean(baseline_64["Data"])],
                }
            ),
        ],
        ignore_index=True,
    )
    ngat_64 = pd.concat(
        [
            ngat_64,
            pd.DataFrame(
                {
                    "Benchmark": ["Ave."],
                    "Data": [harmonic_mean(ngat_64["Data"])],
                }
            ),
        ],
        ignore_index=True,
    )
    
    bar1 = plt.bar(
        r1,
        baseline_32["Data"],
        width=bar_width,
        label="baseline (32 MSHR)",
        color="#C3D9F1",
        edgecolor="black",
        linewidth=1.5,
    )
    bar1_ngat = plt.bar(
        r4,
        ngat_32["Data"],
        width=bar_width,
        color="#C3D9F1",
        edgecolor="black",
        linewidth=1.5,
        hatch="xx",
    )
    bar2 = plt.bar(
        r2,
        baseline_48["Data"],
        width=bar_width,
        label="baseline (48 MSHR)",
        color="#5D73A1",
        edgecolor="black",
        linewidth=1.5,
    )
    bar2_ngat = plt.bar(
        r5,
        ngat_48["Data"],
        width=bar_width,
        color="#5D73A1",
        edgecolor="black",
        linewidth=1.5,
        hatch="xx",
    )
    bar3 = plt.bar(
        r3,
        baseline_64["Data"],
        width=bar_width,
        label="baseline (64 MSHR)",
        color="#313A5B",
        edgecolor="black",
        linewidth=1.5,
    )
    bar3_ngat = plt.bar(
        r6,
        ngat_64["Data"],
        width=bar_width,
        color="#313A5B",
        edgecolor="black",
        linewidth=1.5,
        hatch="xx",
    )

    for bar in bar1 + bar2 + bar3 + bar1_ngat + bar2_ngat + bar3_ngat:
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

    plt.xlim(min(r1) - bar_width, max(r6) + bar_width)
    plt.xticks(
        [r + 2.5 * bar_width for r in r1],
        [get_short_name(benchmarks[i]) for i in range(len(benchmarks))] + ["Ave."],
        fontsize=28,
        fontweight="bold",
    )
    plt.ylabel("Speedup", fontsize=28, fontweight="bold")
    plt.yticks(
        np.arange(0, 8.1, 2),
        fontsize=28,
        fontweight="bold",
    )
    plt.ylim(0, 8)
    
    legend_mshr = [
    Patch(facecolor="#8D2E2C", edgecolor="black", linewidth=1.5, label="32 MSHR"),
    Patch(facecolor="#C3D9F1", edgecolor="black", linewidth=1.5, label="48 MSHR"),
    Patch(facecolor="#5D73A1", edgecolor="black", linewidth=1.5, label="64 MSHR"),
]

    # --- 纹理图例：代表方案类型 ---
    legend_type = [
        Patch(facecolor="white", edgecolor="black", linewidth=1.5, label="Baseline"),
        Patch(facecolor="white", edgecolor="black", linewidth=1.5, hatch="xx", label="ngAT"),
    ]

    leg1 = plt.legend(
        handles=legend_mshr,
        loc="upper left",
        bbox_to_anchor=(0.1, 1.0),
        bbox_transform=plt.gcf().transFigure,
        frameon=True, fancybox=True, framealpha=0.7,
        ncol=3,
        prop={"weight": "bold", "size": 24},
        # title="MSHR Entries",
        title_fontproperties={"weight": "bold", "size": 22},
    )
    leg2 = plt.legend(
        handles=legend_type,
        loc="upper right",
        bbox_to_anchor=(0.8, 1.0),
        bbox_transform=plt.gcf().transFigure,
        frameon=True, fancybox=True, framealpha=0.7,
        ncol=2,
        prop={"weight": "bold", "size": 24},
        # title="Configuration",
        title_fontproperties={"weight": "bold", "size": 22},
    )
    plt.gca().add_artist(leg1)
    
    plt.tight_layout(rect=[0, 0, 1, 0.95])
    plt.grid(axis="y", alpha=0.3)
    plt.axhline(y=1, color="red", linewidth=0.8, linestyle="--")

    ax = plt.gca()

    # 设置图的边框加粗
    for spine in ax.spines.values():
        spine.set_linewidth(1.75)  # 设置边框宽度为 2.5，可根据需要调整

    output_file = os.path.join(
        out_dir, "sensitivity_mshr"
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
    ngat_32     = pd.DataFrame(columns=["Benchmark", "Data"])
    baseline_48 = pd.DataFrame(columns=["Benchmark", "Data"])
    ngat_48     = pd.DataFrame(columns=["Benchmark", "Data"])
    baseline_64 = pd.DataFrame(columns=["Benchmark", "Data"])
    ngat_64     = pd.DataFrame(columns=["Benchmark", "Data"])

    for benchmark in get_high_mpki_benchmarks():
        def append_row(df, benchmark, input_dir):
            perf_data = collect_performance_data(benchmark_name=benchmark, input_dir=input_dir)
            return pd.concat(
                [df, pd.DataFrame({"Benchmark": [benchmark], "Data": [perf_data]})],
                ignore_index=True,
            )

        baseline_32 = append_row(baseline_32, benchmark, "../../final_data/final_baseline")
        ngat_32     = append_row(ngat_32,     benchmark, "../../final_data/final_ngat_4_mshr")
        baseline_48 = append_row(baseline_48,     benchmark, "../../final_data/final_baseline_48MSHR")
        ngat_48     = append_row(ngat_48,     benchmark, "../../final_data/final_ngat_48MSHR")
        baseline_64 = append_row(baseline_64,     benchmark, "../../final_data/final_baseline_64MSHR")
        ngat_64     = append_row(ngat_64,     benchmark, "../../final_data/final_ngat_adaptive")

    plot_normalized_time(
        baseline_32=baseline_32.copy(),
        ngat_32=ngat_32.copy(),
        baseline_48=baseline_48.copy(),
        ngat_48=ngat_48.copy(),
        baseline_64=baseline_64.copy(),
        ngat_64=ngat_64.copy(),
        out_dir=args.outDir,
    )