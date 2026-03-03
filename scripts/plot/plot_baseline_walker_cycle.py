import argparse
import os
import re
import pandas as pd
import numpy as np
import matplotlib.pyplot as plt
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


def collect_performance_data(
    benchmark_name: str,
    input_dir: str,
) -> float:
    """
    Collects performance data from the specified input directory.

    Args:
        benchmark_name (str): The name of the benchmark.
        input_dir (str): The directory containing performance data files.

    Returns:
        list: A list of dictionaries containing performance data.
    """
    performance_data = 0

    file_path = os.path.join(input_dir, f"{benchmark_name}.csv")

    if not os.path.exists(file_path):
        print(f"Performance data file {file_path} does not exist.")

        return performance_data

    # read the CSV file and collect performance data
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
            print(
                f"Warning: kernel time (force stop) for {benchmark_name} is {performance_data}."
            )

            break

    return performance_data

def get_performance(walker: int, cycle: int) -> pd.DataFrame:
    performance_data = pd.DataFrame(
        columns=["Benchmark", "Data"],
    )

    for benchmark in get_benchmarks():
        perf_data = collect_performance_data(
            benchmark_name=benchmark,
            input_dir=os.path.join(input_dir, f"baseline-{walker}walker-{cycle}cycle"),
        )

        performance_data = pd.concat(
            [
                performance_data,
                pd.DataFrame(
                    {
                        "Benchmark": [benchmark],
                        "Data": [perf_data],
                    }
                ),
            ],
            ignore_index=True,
        )

    return performance_data

def plot_lowmpki_normalized_time(
    walker: list,
    cycle: list,
    result: dict,
    out_dir: str,
) -> None:
    """
    绘制折线图：横轴为 Cycle (Latency)，纵轴为相对于 (16, 200) 的 Speedup。
    不同颜色的线代表不同的 Walker 数量。
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

    plt.figure(figsize=(10, 8), dpi=300)

    benchmarks = get_benchmarks()
    filter_benchmarks = [b for b in benchmarks if b in low_mpki_benchmarks]

    result = {k: v[v['Benchmark'].isin(filter_benchmarks)] for k, v in result.items()}
    
    # 1. 提取数据并计算每个 (walker, cycle) 的综合性能指标
    plot_records = []
    for (w, c), df in result.items():
        # 调用 harmonic_mean 函数处理 Data 列
        h_mean = harmonic_mean(df['Data'])
        if h_mean > 0:
            plot_records.append({'Walker': w, 'Cycle': c, 'Perf': h_mean})
    
    summary_df = pd.DataFrame(plot_records)

    # 2. 确定基准点 (Walker=16, Cycle=200)
    baseline_val = summary_df[(summary_df['Walker'] == 16) & (summary_df['Cycle'] == 200)]['Perf']
    
    if baseline_val.empty:
        print("Warning: Baseline (16, 200) not found. Using the first available configuration as baseline.")
        # 如果找不到基准点，防止程序崩溃，取第一条数据
        base_perf = summary_df['Perf'].iloc[0] if not summary_df.empty else 1.0
    else:
        base_perf = baseline_val.values[0]

    # 计算 Speedup (假设 Data 是耗时，Speedup = Baseline_Time / Current_Time)
    summary_df['Speedup'] = base_perf / summary_df['Perf']

    ax = plt.gca()

    # 设置边框加粗
    for spine in ax.spines.values():
        spine.set_linewidth(1.75)

    # --- 修改处：现在按 Walker 分组，横轴是 Cycle ---
    unique_walkers = sorted(list(set(walker)))
    markers = ['o', 's', '^', 'D', 'v', 'p', '*', 'h'] 

    my_colors = [
        '#e97232', '#215f9a', '#3b7d23', '#d62728', 
        '#9467bd', '#8c564b', '#e377c2', '#7f7f7f'
    ]

    for i, w in enumerate(unique_walkers):
        # 筛选特定 Walker 数量的所有数据点，并按 Cycle 排序
        subset = summary_df[summary_df['Walker'] == w].sort_values('Cycle')
        if not subset.empty:
            plt.plot(
                subset['Cycle'], 
                subset['Speedup'], 
                label=f'{w} walkers', 
                marker=markers[i % len(markers)], 
                color=my_colors[i % len(my_colors)],
                linewidth=5, 
                markersize=16
            )

    # 4. 图表装饰
    plt.axhline(y=1, color="red", linewidth=3, linestyle="--")
    plt.xlabel('page table walk latency (cycles)', fontsize=34, fontweight='bold')
    plt.ylabel('Speedup', fontsize=34, fontweight='bold')

    plt.xticks(
            np.arange(200, 801, 100),
            fontsize=34,
            fontweight="bold",
        )
    plt.yticks(
        np.arange(0.4, 1.11, 0.1),
        fontsize=34,
        fontweight="bold",
    )
    plt.ylim(0.5, 1.1)
    # plt.yticks(
    #     np.arange(0, 3.6, 0.5),
    #     fontsize=34,
    #     fontweight="bold",
    # )
    # plt.ylim(0, 3.5)
    
    plt.grid(True, linestyle=':', alpha=0.75)
    # plt.legend(
    #     loc="upper center",
    #     ncol=3,
    #     bbox_to_anchor=(0.5, 1),
    #     bbox_transform=plt.gcf().transFigure,  # 使用图形坐标系
    #     frameon=True,
    #     fancybox=True,
    #     framealpha=0.7,
    #     prop={"weight": "bold", "size": 20},
    # )
    plt.tight_layout(rect=[0, 0, 1, 1])
    
    # # 确保横轴刻度显示实际存在的 Cycle 值
    # plt.xticks(sorted(list(set(cycle))))

    # 5. 保存文件
    output_file = os.path.join(out_dir, "baseline_walker_latency_speedup_lmpki")
    plt.savefig(output_file + ".png", dpi=300)
    plt.savefig(output_file + ".pdf", dpi=300)
    print(f"Plot saved to {output_file}")

    # 6. 单独生成一个图例文件
    plt.figure(figsize=(10, 1), dpi=300)
    for i, w in enumerate(unique_walkers):
        plt.plot([], [], label=f'{w} walkers', marker=markers[i % len(markers)], color=my_colors[i % len(my_colors)], linewidth=2, markersize=8)
    plt.legend(
        ncol=4,
        loc="center",
        frameon=True,
        fancybox=True,
        framealpha=0.7,
        prop={"weight": "bold", "size": 20},
    )
    plt.axis('off')
    legend_output_file = os.path.join(out_dir, "baseline_walker_latency_speedup_legend")
    plt.savefig(legend_output_file + ".png", dpi=300, bbox_inches='tight')
    plt.savefig(legend_output_file + ".pdf", dpi=300, bbox_inches='tight')

def plot_highmpki_normalized_time(
    walker: list,
    cycle: list,
    result: dict,
    out_dir: str,
) -> None:
    """
    绘制折线图：横轴为 Cycle (Latency)，纵轴为相对于 (16, 200) 的 Speedup。
    不同颜色的线代表不同的 Walker 数量。
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

    plt.figure(figsize=(10, 8), dpi=300)

    benchmarks = get_benchmarks()
    filter_benchmarks = [b for b in benchmarks if b in high_mpki_benchmarks]

    result = {k: v[v['Benchmark'].isin(filter_benchmarks)] for k, v in result.items()}
    
    # 1. 提取数据并计算每个 (walker, cycle) 的综合性能指标
    plot_records = []
    for (w, c), df in result.items():
        # 调用 harmonic_mean 函数处理 Data 列
        h_mean = harmonic_mean(df['Data'])
        if h_mean > 0:
            plot_records.append({'Walker': w, 'Cycle': c, 'Perf': h_mean})
    
    summary_df = pd.DataFrame(plot_records)

    # 2. 确定基准点 (Walker=16, Cycle=200)
    baseline_val = summary_df[(summary_df['Walker'] == 16) & (summary_df['Cycle'] == 200)]['Perf']
    
    if baseline_val.empty:
        print("Warning: Baseline (16, 200) not found. Using the first available configuration as baseline.")
        # 如果找不到基准点，防止程序崩溃，取第一条数据
        base_perf = summary_df['Perf'].iloc[0] if not summary_df.empty else 1.0
    else:
        base_perf = baseline_val.values[0]

    # 计算 Speedup (假设 Data 是耗时，Speedup = Baseline_Time / Current_Time)
    summary_df['Speedup'] = base_perf / summary_df['Perf']

    ax = plt.gca()

    # 设置边框加粗
    for spine in ax.spines.values():
        spine.set_linewidth(1.75)

    # --- 修改处：现在按 Walker 分组，横轴是 Cycle ---
    unique_walkers = sorted(list(set(walker)))
    markers = ['o', 's', '^', 'D', 'v', 'p', '*', 'h'] 

    my_colors = [
        '#e97232', '#215f9a', '#3b7d23', '#d62728', 
        '#9467bd', '#8c564b', '#e377c2', '#7f7f7f'
    ]

    for i, w in enumerate(unique_walkers):
        # 筛选特定 Walker 数量的所有数据点，并按 Cycle 排序
        subset = summary_df[summary_df['Walker'] == w].sort_values('Cycle')
        if not subset.empty:
            plt.plot(
                subset['Cycle'], 
                subset['Speedup'], 
                label=f'{w} walkers', 
                marker=markers[i % len(markers)], 
                color=my_colors[i % len(my_colors)],
                linewidth=5, 
                markersize=16
            )

    # 4. 图表装饰
    plt.axhline(y=1, color="red", linewidth=3, linestyle="--")
    plt.xlabel('page table walk latency (cycles)', fontsize=34, fontweight='bold')
    plt.ylabel('Speedup', fontsize=34, fontweight='bold')

    plt.xticks(
            np.arange(200, 801, 100),
            fontsize=34,
            fontweight="bold",
        )
    plt.yticks(
        np.arange(0, 5.1, 1),
        fontsize=34,
        fontweight="bold",
    )
    plt.ylim(0, 5)
    
    plt.grid(True, linestyle=':', alpha=0.75)
    # plt.legend(
    #     loc="upper center",
    #     ncol=3,
    #     bbox_to_anchor=(0.5, 1),
    #     bbox_transform=plt.gcf().transFigure,  # 使用图形坐标系
    #     frameon=True,
    #     fancybox=True,
    #     framealpha=0.7,
    #     prop={"weight": "bold", "size": 20},
    # )
    plt.tight_layout(rect=[0, 0, 1, 1])
    
    # # 确保横轴刻度显示实际存在的 Cycle 值
    # plt.xticks(sorted(list(set(cycle))))

    # 5. 保存文件
    output_file = os.path.join(out_dir, "baseline_walker_latency_speedup_hmpki")
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

    input_dir = "../../data/baseline-walker-cycle"

    pattern = re.compile(r"-(\d+)walker-(\d+)cycle")

    walkers = []
    cycles = []

    results = {}

    for name in os.listdir(input_dir):
        full_path = os.path.join(input_dir, name)
        if os.path.isdir(full_path):
            match = pattern.search(name)
        if match:
            walker = int(match.group(1))
            cycle = int(match.group(2))

            walkers.append(walker)
            cycles.append(cycle)

            performance_data = get_performance(walker, cycle)

            results[(walker, cycle)] = performance_data

    plot_lowmpki_normalized_time(
        walker=walkers,
        cycle=cycles,
        result=results.copy(),
        out_dir=args.outDir,
    )
    plot_highmpki_normalized_time(
        walker=walkers,
        cycle=cycles,
        result=results.copy(),
        out_dir=args.outDir,
    )

            

