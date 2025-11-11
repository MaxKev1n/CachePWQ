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

        noc_name = ""
        if type == "tlb":
            noc_name = "L1TLBToL2TLBNoC"
        elif type == "memory":
            noc_name = "L1ToL2NoC"
        else:
            assert False, "Unsupported type"

        if "BookSimNocTraffic" in what and noc_name in where:
            split_what = what.split(" ")
            src_node = int(split_what[2].split(":")[0])
            dst_node = int(split_what[2].split(":")[-1])

            if src_node in nodes_mappding and dst_node in nodes_mappding:
                src_idx = nodes_mappding[src_node]
                dst_idx = nodes_mappding[dst_node]

                performance_data[src_idx, dst_idx] = value

    return performance_data


def plot_heat_map(
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
    plt.figure(figsize=(16, 10), dpi=300)

    # normalize
    for i in range(data.shape[0]):
        row_sum = np.sum(data[i, :])
        if row_sum > 0:
            data[i, :] = data[i, :] / row_sum

    heatmap = sns.heatmap(
        data,
        annot=False,  # 不显示格子内数字
        cmap="OrRd",  # 明显的颜色方案
        cbar=True,  # 显示颜色条
        cbar_kws={
            "orientation": "horizontal",  # 水平放置
            "pad": 0.05,  # 与图的间距
            "shrink": 0.95,  # 缩放比例（整体长度）
            "aspect": 60,  # ✅ 控制颜色条厚度（宽度）——值越大越细
        },
        square=True,
        linewidths=0.6,
        linecolor="black",
        annot_kws={"size": 16},
        # norm=plt.matplotlib.colors.PowerNorm(gamma=0.4),
    )

    # ✅ 获取 colorbar 对象
    cbar = heatmap.collections[0].colorbar

    # ✅ 修改颜色条刻度文字样式
    cbar.ax.tick_params(labelsize=16, width=1.2, length=6)  # 刻度字号、线宽、刻度线长度
    cbar.ax.xaxis.label.set_size(14)  # 颜色条标题大小
    cbar.ax.xaxis.label.set_weight("bold")

    # ✅ 修改字体和颜色
    for label in cbar.ax.get_xticklabels():  # 如果是水平颜色条
        label.set_fontname("Arial")  # 字体
        label.set_color("black")  # 文字颜色

    # 设置标题和标签
    # plt.xlabel(
    #     "L2 Cache Partition Index",
    #     fontsize=20,
    #     fontweight="bold",
    # )
    heatmap.set_xticklabels(
        [f"{i}" for i in range(data.shape[1])],
        fontsize=18,
        fontweight="bold",
    )
    heatmap.set_yticklabels(
        [get_short_name(b) for b in get_benchmarks()],
        rotation=0,
        fontsize=16,
        fontweight="bold",
    )

    plt.tight_layout()

    ax = plt.gca()

    # 设置图的边框加粗
    for spine in ax.spines.values():
        spine.set_linewidth(1.75)  # 设置边框宽度为 2.5，可根据需要调整

    output_file = os.path.join(out_dir, f"{config}_{what}_heap_map")
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

    nodes = get_booksim_mem_nodes("MemorySide")

    total_perf_data = np.zeros((len(get_benchmarks()), 32))

    i = 0
    for benchmark in get_benchmarks():
        perf_data = collect_performance_data(
            config="MemorySide",
            type="memory",
            benchmark_name=benchmark,
            input_dir="../../data/MemorySide-2NoC-Booksim",
        )

        if perf_data.sum() == 0:
            print(f"No data for benchmark {benchmark}, skipping plot.")
            continue

        total_perf_data[i, :] = perf_data[192:193, 193:]

        i += 1

    plot_heat_map(
        config="MemorySide",
        what="mmu",
        data=total_perf_data,
        out_dir=args.outDir,
    )
