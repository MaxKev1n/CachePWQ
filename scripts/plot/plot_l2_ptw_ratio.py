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
    all_data = 0
    file_path = os.path.join(input_dir, f"{benchmark_name}.csv")
    if not os.path.exists(file_path):
        print(f"Performance data file {file_path} does not exist.")
        return performance_data
    df = pd.read_csv(file_path)
    for _, row in df.iterrows():
        if "L2_" not in row.iloc[1]:
            continue
        
        if " ptw-read-" in row.iloc[2]:
            performance_data += row.iloc[3]
            all_data += row.iloc[3]
        elif " write-" in row.iloc[2] or " read-" in row.iloc[2]:
            all_data += row.iloc[3]
            
    return performance_data / all_data


def plot_normalized_time(
    baseline: pd.DataFrame,
    opt: pd.DataFrame,
    infinite: pd.DataFrame,
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

    # Ave
    baseline = pd.concat(
        [
            baseline,
            pd.DataFrame(
                {
                    "Benchmark": ["Ave."],
                    "Data": [harmonic_mean(baseline["Data"])],
                }
            ),
        ],
        ignore_index=True,
    )
    opt = pd.concat(
        [
            opt,
            pd.DataFrame(
                {
                    "Benchmark": ["Ave."],
                    "Data": [harmonic_mean(opt["Data"])],
                }
            ),
        ],
        ignore_index=True,
    )
    infinite = pd.concat(
        [
            infinite,
            pd.DataFrame(
                {
                    "Benchmark": ["Ave."],
                    "Data": [harmonic_mean(infinite["Data"])],
                }
            ),
        ],
        ignore_index=True,
    )
    print("Baseline Data:")
    print(baseline)
    print("\nOptimized Data:")
    print(opt)
    print("\nInfinite Walker Data:")
    print(infinite)
    
    bar1 = plt.bar(
        r1,
        baseline["Data"],
        width=bar_width,
        label="baseline",
        color="#C3D9F1",
        edgecolor="black",
        linewidth=1.5,
    )
    bar2 = plt.bar(
        r2,
        opt["Data"],
        width=bar_width,
        label="NB-Walker + AMR",
        color="#5D73A1",
        edgecolor="black",
        linewidth=1.5,
    )
    bar3 = plt.bar(
        r3,
        infinite["Data"],
        width=bar_width,
        label="infinite walkers",
        color="#313A5B",
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
        [r +  1 * bar_width for r in r1],
        [get_short_name(benchmarks[i]) for i in range(len(benchmarks))] + ["HMean"],
        fontsize=36,
        fontweight="bold",
    )
    plt.ylabel("Ratio of L2\n req. for PTWs", fontsize=36, fontweight="bold")
    plt.yticks(
        np.arange(0, 0.41, 0.1),
        fontsize=36,
        fontweight="bold",
    )
    plt.ylim(0, 0.4)
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
    # plt.axhline(y=1, color="red", linewidth=0.8, linestyle="--")

    ax = plt.gca()

    # 设置图的边框加粗
    for spine in ax.spines.values():
        spine.set_linewidth(1.75)  # 设置边框宽度为 2.5，可根据需要调整

    output_file = os.path.join(
        out_dir, "memory_traffic"
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
    baseline = pd.DataFrame(columns=["Benchmark", "Data"])
    opt     = pd.DataFrame(columns=["Benchmark", "Data"])
    infinite   = pd.DataFrame(columns=["Benchmark", "Data"])

    for benchmark in get_high_mpki_benchmarks():
        def append_row(df, benchmark, input_dir):
            perf_data = collect_performance_data(benchmark_name=benchmark, input_dir=input_dir)
            return pd.concat(
                [df, pd.DataFrame({"Benchmark": [benchmark], "Data": [perf_data]})],
                ignore_index=True,
            )

        baseline = append_row(baseline, benchmark, "../../final_final_data/baseline")
        opt     = append_row(opt,     benchmark, "../../final_final_data/nbwalker-full")
        infinite   = append_row(infinite,   benchmark, "../../final_final_data/infinitewalker")

    plot_normalized_time(
        baseline=baseline.copy(),
        opt=opt.copy(),
        infinite=infinite.copy(),
        out_dir=args.outDir,
    )