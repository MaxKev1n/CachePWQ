import argparse
import os
import re
import pandas as pd
import numpy as np
import matplotlib.pyplot as plt
from matplotlib.lines import Line2D
from benchmark import get_benchmarks, get_short_name
from benchmark import low_mpki_benchmarks, high_mpki_benchmarks

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


def read_data_to_dataframe(benchmark_name: str, input_dir: str):
    """
    按行读取文件，读取前 512 行，分割字符串，
    并将第二个和第三个数据转成浮点数存入 pandas DataFrame。
    """
    file_path = os.path.join(input_dir, f"{benchmark_name}.trace")

    data_list = []
    try:
        with open(file_path, 'r', encoding='utf-8') as f:
            for i, line in enumerate(f):
                if i >= 2000:
                    break
                
                # 去除换行符并按逗号分割
                parts = line.strip().split(',')
                
                # 确保每行至少有 3 个元素（索引, 数据1, 数据2）
                if len(parts) >= 3:
                    try:
                        # 提取第 2 个和第 3 个数据（索引为 1 和 2）
                        val1 = float(parts[1].strip()) / 32 * 100
                        val2 = float(parts[2].strip()) / 64 * 100
                        data_list.append([val1, val2])
                    except ValueError:
                        # 跳过无法转换为浮点数的行
                        continue
                        
        # 转换为 DataFrame 并命名列名
        df = pd.DataFrame(data_list, columns=['MSHR', 'PWQ'])
        return df
    
    except FileNotFoundError:
        print(f"错误：找不到文件 {file_path}")
        return pd.DataFrame()

def plot_data(
    df: pd.DataFrame,
    out_dir: str,
):
    """
    plot the histogram of latency distribution.
    """
    if not os.path.exists(out_dir):
        os.makedirs(out_dir)

    if df.empty:
        print("DataFrame 为空，无法绘图。")
        return

    # Set Arial font family
    plt.rcParams["font.family"] = "Arial"
    # For macOS, you might need to explicitly set the font file
    plt.rcParams["font.sans-serif"] = ["Arial"]
    plt.rcParams["mathtext.fontset"] = "custom"
    plt.rcParams["mathtext.rm"] = "Arial"
    plt.rcParams["mathtext.it"] = "Arial:italic"
    plt.rcParams["mathtext.bf"] = "Arial:bold"

    plt.figure(figsize=(8, 3.5), dpi=300)
    
    # 绘制两组数据
    plt.plot(df.index, df['MSHR'], label='MSHR', marker='.', linestyle='-', color="#C3D9F1")
    plt.plot(df.index, df['PWQ'], label='PWQ', marker='.', linestyle='--', color="#313A5B")
    
    # 添加图表信息
    plt.xlabel(r'Sampling Interval Index', fontsize=28, fontweight='bold')
    plt.ylabel('Occupancy (%)', fontsize=28, fontweight='bold')
    plt.xlim(-100, 2100)
    plt.xticks(
        [i for i in range(0, 2100, 400)],
        [str(i) for i in range(0, 2100, 400)],
        fontsize=28,
        fontweight="bold",
        # rotation=90,
    )
    plt.ylim(0, 100)
    plt.yticks(
        [i for i in range(0, 101, 20)],
        [str(i) for i in range(0, 101, 20)],
        fontsize=28,
        fontweight="bold",
    )
    # plt.legend()
    plt.grid(True, linestyle=':', alpha=0.6) # 显示网格

    ax = plt.gca()

    for spine in ax.spines.values():
        spine.set_linewidth(1.75)  # 设置边框宽度为 2.5，可根据需要调整
    
    # 显示图形
    plt.tight_layout()
    output_file = os.path.join(
        out_dir, f"{benchmark}_mshr_pwq_utilization"
    )
    plt.savefig(output_file + ".png", dpi=300)
    plt.savefig(output_file + ".pdf", dpi=300)
    print(f"Plot saved to {output_file}")

    # 6. 单独生成一个图例文件
    plt.figure(figsize=(10, 1), dpi=300)
    legend_elements = [
        Line2D([0], [0], label='MSHR Occupancy', marker='.', linestyle='-', color="#C3D9F1", lw=2),
        Line2D([0], [0], label='Page Walk Queue Occupancy', marker='.', linestyle='--', color="#313A5B", lw=2),
    ]
    plt.legend(
        handles=legend_elements,
        ncol=2,
        loc="center",
        frameon=True,
        fancybox=True,
        framealpha=0.7,
        prop={"weight": "bold", "size": 18},
    )
    plt.axis('off')
    legend_output_file = os.path.join(out_dir, "baseline_mshr_pwq_utilization_legend")
    plt.savefig(legend_output_file + ".png", dpi=300, bbox_inches='tight')
    plt.savefig(legend_output_file + ".pdf", dpi=300, bbox_inches='tight')

# --- 使用示例 ---
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
        result = read_data_to_dataframe(
            benchmark_name=benchmark,
            input_dir="../../data/MSHR-PWQ-Utilization",
        )

    
        # 2. 调用绘图函数
        plot_data(result, args.outDir)