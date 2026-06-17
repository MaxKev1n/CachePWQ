import argparse
import os
import pandas as pd
import numpy as np
import matplotlib.pyplot as plt
from benchmark import get_high_mpki_benchmarks, get_short_name, low_mpki_benchmarks, high_mpki_benchmarks


def geometric_mean(df: pd.DataFrame) -> float:
    data = df.tolist()
    if not data or any(x <= 0 for x in data):
        return 0.0
    product = np.prod(data)
    return product ** (1 / len(data))


def harmonic_mean(df: pd.DataFrame) -> float:
    data = df.tolist()
    if not data or any(x <= 0 for x in data):
        return 0.0
    reciprocal_sum = sum(1 / x for x in data)
    return len(data) / reciprocal_sum


def collect_performance_data(benchmark_name: str, input_dir: str) -> float:
    file_path = os.path.join(input_dir, f"{benchmark_name}.csv")
    if not os.path.exists(file_path):
        print(f"Performance data file {file_path} does not exist.")
        return 0

    df = pd.read_csv(file_path)

    kernel_time = 0
    for _, row in df.iterrows():
        if row.iloc[1] == " driver" and row.iloc[2] == " kernel_time":
            kernel_time = row.iloc[3]
            if kernel_time == 0:
                print(f"Warning: kernel time for {benchmark_name} is zero.")
                continue
            else:
                break
        if (
            row.iloc[1] == " GPU1.CommandProcessor"
            and row.iloc[2] == " kernel_time (force stop) 0"
        ):
            kernel_time = row.iloc[3]
            print(f"Warning: kernel time (force stop) for {benchmark_name} is {kernel_time}.")
            break
    
    inst_count = 0
    for _, row in df.iterrows():
        if row.iloc[2] == " inst_count":
            inst_count += row.iloc[3]
        
    return float(inst_count) / float(kernel_time) if kernel_time != 0 else 0

def plot_normalized_time(
    baseline_4K: pd.DataFrame,
    baseline_32K: pd.DataFrame,
    baseline_64K: pd.DataFrame,
    baseline_128K: pd.DataFrame,
    baseline_2M: pd.DataFrame,
    opt_4K: pd.DataFrame,
    opt_32K: pd.DataFrame,
    opt_64K: pd.DataFrame,
    opt_128K: pd.DataFrame,
    opt_2M: pd.DataFrame,
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

    benchmarks = get_high_mpki_benchmarks()

    bar_width = 0.15
    r1 = np.arange(len(benchmarks) + 1) * (5 * bar_width + 0.2)
    r2 = [x + bar_width for x in r1]
    r3 = [x + bar_width for x in r2]
    r4 = [x + bar_width for x in r3]
    r5 = [x + bar_width for x in r4]

    # Normalize the time
    opt_4K["Data"] = [
        (
            opt_4K["Data"][i] / baseline_4K["Data"][i]
            if opt_4K["Data"][i] != 0 and baseline_4K["Data"][i] != 0
            else 0
        )
        for i in range(len(benchmarks))
    ]
    opt_32K["Data"] = [
        (
            opt_32K["Data"][i] / baseline_32K["Data"][i]
            if opt_32K["Data"][i] != 0 and baseline_32K["Data"][i] != 0
            else 0
        )
        for i in range(len(benchmarks))
    ]
    opt_64K["Data"] = [
        (
            opt_64K["Data"][i] / baseline_64K["Data"][i]
            if opt_64K["Data"][i] != 0 and baseline_64K["Data"][i] != 0
            else 0
        )
        for i in range(len(benchmarks))
    ]
    opt_128K["Data"] = [
        (
            opt_128K["Data"][i] / baseline_128K["Data"][i]
            if opt_128K["Data"][i] != 0 and baseline_128K["Data"][i] != 0
            else 0
        )
        for i in range(len(benchmarks))
    ]
    opt_2M["Data"] = [
        (
            opt_2M["Data"][i] / baseline_2M["Data"][i]
            if opt_2M["Data"][i] != 0 and baseline_2M["Data"][i] != 0
            else 0
        )
        for i in range(len(benchmarks))
    ]
    
    print("opt_4K Data:", opt_4K["Data"].tolist())
    print("opt_32K Data:", opt_32K["Data"].tolist())
    print("opt_64K Data:", opt_64K["Data"].tolist())
    print("opt_128K Data:", opt_128K["Data"].tolist())
    print("opt_2M Data:", opt_2M["Data"].tolist())

    opt_4K = pd.concat(
        [
            opt_4K,
            pd.DataFrame(
                {
                    "Benchmark": ["Ave."],
                    "Data": [harmonic_mean(opt_4K["Data"])],
                }
            ),
        ],
        ignore_index=True,
    )
    opt_32K = pd.concat(
        [
            opt_32K,
            pd.DataFrame(
                {
                    "Benchmark": ["Ave."],
                    "Data": [harmonic_mean(opt_32K["Data"])],
                }
            ),
        ],
        ignore_index=True,
    )
    opt_64K = pd.concat(
        [
            opt_64K,
            pd.DataFrame(
                {
                    "Benchmark": ["Ave."],
                    "Data": [harmonic_mean(opt_64K["Data"])],
                }
            ),
        ],
        ignore_index=True,
    )
    opt_128K = pd.concat(
        [
            opt_128K,
            pd.DataFrame(
                {
                    "Benchmark": ["Ave."],
                    "Data": [harmonic_mean(opt_128K["Data"])],
                }
            ),
        ],
        ignore_index=True,
    )
    opt_2M = pd.concat(
        [
            opt_2M,
            pd.DataFrame(
                {
                    "Benchmark": ["Ave."],
                    "Data": [harmonic_mean(opt_2M["Data"])],
                }
            ),
        ],
        ignore_index=True,
    )

    bar1 = plt.bar(
        r1,
        opt_4K["Data"],
        width=bar_width,
        label="4KB",
        color="#8D2E2C",
        edgecolor="black",
        linewidth=1.5,
    )
    bar2 = plt.bar(
        r2,
        opt_32K["Data"],
        width=bar_width,
        label="32KB",
        color="#FDE4EA",
        edgecolor="black",
        linewidth=1.5,
    )
    bar3 = plt.bar(
        r3,
        opt_64K["Data"],
        width=bar_width,
        label="64KB",
        color="#C3D9F1",
        edgecolor="black",
        linewidth=1.5,
    )
    bar4 = plt.bar(
        r4,
        opt_128K["Data"],
        width=bar_width,
        label="128KB",
        color="#5D73A1",
        edgecolor="black",
        linewidth=1.5,
    )
    bar5 = plt.bar(
        r5,
        opt_2M["Data"],
        width=bar_width,
        label="2MB",
        color="#313A5B",
        edgecolor="black",
        linewidth=1.5,
    )

    for bar in bar1 + bar2 + bar3 + bar4 + bar5:
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

    plt.xlim(min(r1) - bar_width, max(r5) + bar_width)
    plt.xticks(
        [r + 2 * bar_width for r in r1],
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
        ncol=5,
        bbox_to_anchor=(0.5, 1),
        bbox_transform=plt.gcf().transFigure,  # 使用图形坐标系
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
        out_dir, "sensitivity_pagesize"
    )
    plt.savefig(output_file + ".png")
    plt.savefig(output_file + ".pdf")
    print(f"Plot saved to {output_file}")



if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="Parse csv file.")
    parser.add_argument("--outDir", required=True, type=str,
                        help="Directory path to save the output plots.")
    args = parser.parse_args()

    baseline_4K = pd.DataFrame(columns=["Benchmark", "Data"])
    baseline_32K = pd.DataFrame(columns=["Benchmark", "Data"])
    baseline_64K = pd.DataFrame(columns=["Benchmark", "Data"])
    baseline_128K = pd.DataFrame(columns=["Benchmark", "Data"])
    baseline_2M = pd.DataFrame(columns=["Benchmark", "Data"])
    opt_4K = pd.DataFrame(columns=["Benchmark", "Data"])
    opt_32K = pd.DataFrame(columns=["Benchmark", "Data"])
    opt_64K = pd.DataFrame(columns=["Benchmark", "Data"])
    opt_128K = pd.DataFrame(columns=["Benchmark", "Data"])
    opt_2M = pd.DataFrame(columns=["Benchmark", "Data"])

    for benchmark in get_high_mpki_benchmarks():
        def append_row(df, benchmark, input_dir):
            perf_data = collect_performance_data(benchmark_name=benchmark, input_dir=input_dir)
            return pd.concat(
                [df, pd.DataFrame({"Benchmark": [benchmark], "Data": [perf_data]})],
                ignore_index=True,
            )

        baseline_4K = append_row(baseline_4K, benchmark, "../../final_final_data/sensitivity_pagesize/4KB/BaselineMMU_4KB_1B")
        baseline_32K = append_row(baseline_32K, benchmark, "../../final_final_data/sensitivity_pagesize/32KB/BaselineMMU_32KB_1B")
        baseline_64K  = append_row(baseline_64K, benchmark, "../../final_final_data/sensitivity_pagesize/64KB/BaselineMMU_64KB_1B")
        baseline_128K = append_row(baseline_128K, benchmark, "../../final_final_data/sensitivity_pagesize/128KB/BaselineMMU_128KB_1B")
        baseline_2M  = append_row(baseline_2M, benchmark, "../../final_final_data/sensitivity_pagesize/2MB/BaselineMMU_2MB_1B")
        opt_4K     = append_row(opt_4K,     benchmark, "../../final_final_data/sensitivity_pagesize/4KB/nbwalkerfull_4KB_1B")
        opt_32K    = append_row(opt_32K,    benchmark, "../../final_final_data/sensitivity_pagesize/32KB/nbwalkerfull_32KB_1B")
        opt_64K   = append_row(opt_64K,    benchmark, "../../final_final_data/sensitivity_pagesize/64KB/nbwalkerfull_64KB_1B")
        opt_128K  = append_row(opt_128K,   benchmark, "../../final_final_data/sensitivity_pagesize/128KB/nbwalkerfull_128KB_1B")
        opt_2M    = append_row(opt_2M,     benchmark, "../../final_final_data/sensitivity_pagesize/2MB/nbwalkerfull_2MB_1B")

    plot_normalized_time(
        baseline_4K=baseline_4K,
        baseline_32K=baseline_32K,
        baseline_64K=baseline_64K,
        baseline_128K=baseline_128K,
        baseline_2M=baseline_2M,
        opt_4K=opt_4K,
        opt_32K=opt_32K,
        opt_64K=opt_64K,
        opt_128K=opt_128K,
        opt_2M=opt_2M,
        out_dir=args.outDir,
    )