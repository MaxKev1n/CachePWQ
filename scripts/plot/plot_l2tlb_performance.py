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
    baseline_1GPC: pd.DataFrame,
    ngat_1GPC: pd.DataFrame,
    baseline_2GPC: pd.DataFrame,
    ngat_2GPC: pd.DataFrame,
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

    plt.figure(figsize=(20, 4.75), dpi=300)

    benchmarks = get_high_mpki_benchmarks()

    bar_width = 0.1
    r1 = np.arange(len(benchmarks) + 1) * (2 * bar_width + 0.2)
    r2 = [x + bar_width for x in r1]

    # Normalize the time
    ngat_1GPC["Data"] = [
        (
            baseline_1GPC["Data"][i] / ngat_1GPC["Data"][i]
            if ngat_1GPC["Data"][i] != 0 and baseline_1GPC["Data"][i] != 0
            else 0
        )
        for i in range(len(benchmarks))
    ]
    ngat_2GPC["Data"] = [
        (
            baseline_2GPC["Data"][i] / ngat_2GPC["Data"][i]
            if ngat_2GPC["Data"][i] != 0 and baseline_2GPC["Data"][i] != 0
            else 0
        )
        for i in range(len(benchmarks))
    ]
    
    baseline_1GPC["Data"] = [1.0 for _ in range(len(benchmarks))]
    baseline_2GPC["Data"] = [1.0 for _ in range(len(benchmarks))]

    # Ave
    baseline_1GPC = pd.concat(
        [
            baseline_1GPC,
            pd.DataFrame(
                {
                    "Benchmark": ["Ave."],
                    "Data": [harmonic_mean(baseline_1GPC["Data"])],
                }
            ),
        ],
        ignore_index=True,
    )
    baseline_2GPC = pd.concat(
        [
            baseline_2GPC,
            pd.DataFrame(
                {
                    "Benchmark": ["Ave."],
                    "Data": [harmonic_mean(baseline_2GPC["Data"])],
                }
            ),
        ],
        ignore_index=True,
    )
    ngat_1GPC = pd.concat(
        [
            ngat_1GPC,
            pd.DataFrame(
                {
                    "Benchmark": ["Ave."],
                    "Data": [harmonic_mean(ngat_1GPC["Data"])],
                }
            ),
        ],
        ignore_index=True,
    )
    ngat_2GPC = pd.concat(
        [
            ngat_2GPC,
            pd.DataFrame(
                {
                    "Benchmark": ["Ave."],
                    "Data": [harmonic_mean(ngat_2GPC["Data"])],
                }
            ),
        ],
        ignore_index=True,
    )
    
    bar1 = plt.bar(
        r1,
        ngat_1GPC["Data"],
        width=bar_width,
        label="2-level TLB",
        color="#C3D9F1",
        edgecolor="black",
        linewidth=1.5,
    )
    bar2 = plt.bar(
        r2,
        ngat_2GPC["Data"],
        width=bar_width,
        label="3-level TLB",
        color="#5D73A1",
        edgecolor="black",
        linewidth=1.5,
    )
    # bar3 = plt.bar(
    #     r3,
    #     ngat_1GPC["Data"],
    #     width=bar_width,
    #     label="2-level TLB with nbWalker + ARM",
    #     color="#5D73A1",
    #     edgecolor="black",
    #     linewidth=1.5,
    # )
    # bar4 = plt.bar(
    #     r4,
    #     ngat_2GPC["Data"],
    #     width=bar_width,
    #     label="3-level TLB with nbWalker + ARM",
    #     color="#313A5B",
    #     edgecolor="black",
    #     linewidth=1.5,
    # )


    for bar in bar1 + bar2:
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

    plt.xlim(min(r1) - bar_width, max(r2) + bar_width)
    plt.xticks(
        [r +  0.5 * bar_width for r in r1],
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
        out_dir, "sensitivity_tlb"
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
    baseline_l2tlb = pd.DataFrame(columns=["Benchmark", "Data"])
    ngat_l2tlb     = pd.DataFrame(columns=["Benchmark", "Data"])
    baseline_l3tlb = pd.DataFrame(columns=["Benchmark", "Data"])
    ngat_l3tlb     = pd.DataFrame(columns=["Benchmark", "Data"])

    for benchmark in get_high_mpki_benchmarks():
        def append_row(df, benchmark, input_dir):
            perf_data = collect_performance_data(benchmark_name=benchmark, input_dir=input_dir)
            return pd.concat(
                [df, pd.DataFrame({"Benchmark": [benchmark], "Data": [perf_data]})],
                ignore_index=True,
            )

        baseline_l2tlb = append_row(baseline_l2tlb, benchmark, "../../final_final_data/L2TLB_baseline")
        ngat_l2tlb     = append_row(ngat_l2tlb,     benchmark, "../../final_final_data/L2TLB_nbwalker")
        baseline_l3tlb = append_row(baseline_l3tlb, benchmark, "../../final_final_data/baseline")
        ngat_l3tlb     = append_row(ngat_l3tlb,     benchmark, "../../final_final_data/nbwalker-full")

    plot_normalized_time(
        baseline_1GPC=baseline_l2tlb.copy(),
        ngat_1GPC=ngat_l2tlb.copy(),
        baseline_2GPC=baseline_l3tlb.copy(),
        ngat_2GPC=ngat_l3tlb.copy(),
        out_dir=args.outDir,
    )