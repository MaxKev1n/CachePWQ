import argparse
import os
import pandas as pd
import numpy as np
import matplotlib.pyplot as plt
import seaborn as sns
from benchmark import (
    get_benchmarks,
    get_short_name,
    get_booksim_tlb_nodes,
    get_booksim_mem_nodes,
)


def collect_performance_data(
    config: str,
    type: str,
    benchmark_name: str,
    input_dir: str,
) -> np.ndarray:
    """
    Collects performance data from the specified input directory.

    Args:
        benchmark_name (str): The name of the benchmark.
        input_dir (str): The directory containing performance data files.

    Returns:
        list: A list of dictionaries containing performance data.
    """
    nodes = []

    if type == "tlb":
        nodes = get_booksim_tlb_nodes(config)
    elif type == "memory":
        nodes = get_booksim_mem_nodes(config)
    else:
        assert False, "Unsupported type"

    nodes_mappding = dict()

    for idx, node in enumerate(nodes):
        nodes_mappding[node] = idx

    performance_data = np.zeros((len(nodes), len(nodes)))

    file_path = os.path.join(input_dir, f"{benchmark_name}.csv")

    if not os.path.exists(file_path):
        print(f"Performance data file {file_path} does not exist.")

        return performance_data

    # read the CSV file and collect performance data
    df = pd.read_csv(file_path)

    for _, row in df.iterrows():
        where = row.iloc[1]
        what = row.iloc[2]
        value = row.iloc[3]

        if "BookSimNocTraffic" in what:
            split_what = what.split(" ")
            src_node = int(split_what[2].split(":")[0])
            dst_node = int(split_what[2].split(":")[-1])

            if src_node in nodes_mappding and dst_node in nodes_mappding:
                src_idx = nodes_mappding[src_node]
                dst_idx = nodes_mappding[dst_node]

                performance_data[src_idx, dst_idx] = value

    return performance_data


def plot_heat_map(
    benchmark: str,
    config: str,
    what: str,
    data: np.ndarray,
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

    # 创建图形
    plt.figure(figsize=(20, 16), dpi=300)

    # 创建热力图
    heatmap = sns.heatmap(
        data,
        annot=False,  # 在格子中显示数值
        cmap="viridis",  # 颜色方案
        cbar=True,  # 显示颜色条
        square=True,  # 保持格子为正方形
        linewidths=0.5,  # 格子间线宽
        linecolor="white",  # 格子间线颜色
        annot_kws={"size": 10},
    )  # 注解文字大小

    # 设置标题和标签
    plt.title("Node-to-Node Accesses", fontsize=14, fontweight="bold", pad=20)
    plt.xlabel("Destination Node ID", fontsize=12)
    plt.ylabel("Source Node ID", fontsize=12)

    plt.tight_layout()

    ax = plt.gca()

    # 设置图的边框加粗
    for spine in ax.spines.values():
        spine.set_linewidth(1.75)  # 设置边框宽度为 2.5，可根据需要调整

    output_file = os.path.join(out_dir, f"{benchmark}_{config}_{what}_heap_map")
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

    for benchmark in get_benchmarks():
        perf_data = collect_performance_data(
            config="SMSide",
            type="tlb",
            benchmark_name=benchmark,
            input_dir="../../data/test",
        )

        if perf_data.sum() == 0:
            print(f"No data for benchmark {benchmark}, skipping plot.")
            continue

        plot_heat_map(
            benchmark=benchmark,
            config="SMSide",
            what="tlb",
            data=perf_data,
            out_dir=args.outDir,
        )

        perf_data = collect_performance_data(
            config="SMSide",
            type="memory",
            benchmark_name=benchmark,
            input_dir="../../data/test",
        )

        if perf_data.sum() == 0:
            print(f"No data for benchmark {benchmark}, skipping plot.")
            continue

        plot_heat_map(
            benchmark=benchmark,
            config="SMSide",
            what="memory",
            data=perf_data,
            out_dir=args.outDir,
        )
