import argparse
import os
import re
import pandas as pd
import numpy as np
import matplotlib.pyplot as plt
from matplotlib.lines import Line2D
from benchmark import get_benchmarks, get_short_name
from benchmark import low_mpki_benchmarks, high_mpki_benchmarks

def parse_frequency(value: str) -> float:
    """Parse frequency values like 1e9, 250KHz, 1GHz into Hz."""
    text = value.strip().lower().replace(" ", "")
    suffixes = {
        "ghz": 1e9,
        "mhz": 1e6,
        "khz": 1e3,
        "hz": 1.0,
    }

    for suffix, multiplier in suffixes.items():
        if text.endswith(suffix):
            frequency = float(text[:-len(suffix)]) * multiplier
            break
    else:
        frequency = float(text)

    if frequency <= 0:
        raise argparse.ArgumentTypeError("Frequency must be positive.")

    return frequency

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
    Parse AMR monitor stderr logs into a DataFrame.

    The first epoch only records that the initial data was collected, so keep it
    as a placeholder row. Later epochs contain one row per GPC.
    """
    file_path = os.path.join(input_dir, f"{benchmark_name}.trace")

    data_list = []
    gpc_epoch_pattern = re.compile(
        r"Epoch\s+(?P<epoch>\d+):\s+GPC\s+(?P<gpc>\d+),\s+"
        r"HP Occupancy\s+(?P<hp_occupancy>\d+(?:\.\d+)?),\s+"
        r"Normal Occupancy\s+(?P<normal_occupancy>\d+(?:\.\d+)?),\s+"
        r"Walk Occupancy\s+(?P<walk_occupancy>\d+(?:\.\d+)?),\s+"
        r"Reserve\s+(?P<reserve>\d+)"
    )

    try:
        with open(file_path, 'r') as f:
            for line in f:
                gpc_match = gpc_epoch_pattern.search(line)
                if not gpc_match:
                    continue

                data_list.append({
                    "Epoch": int(gpc_match.group("epoch")),
                    "GPC": int(gpc_match.group("gpc")),
                    "HP Occupancy": float(gpc_match.group("hp_occupancy")) * 16 / 64 * 100,
                    "Normal Occupancy": float(gpc_match.group("normal_occupancy")) / 32 * 100,
                    "Walk Occupancy": float(gpc_match.group("walk_occupancy")) / 32 * 100,
                    "Reserve": int(gpc_match.group("reserve")) / 32 * 100,
                })

        return pd.DataFrame(
            data_list,
            columns=[
                "Epoch",
                "GPC",
                "HP Occupancy",
                "Normal Occupancy",
                "Walk Occupancy",
                "Reserve",
            ],
        )
    
    except FileNotFoundError:
        print(f"错误：找不到文件 {file_path}")
        return pd.DataFrame()

def plot_data(
    df: pd.DataFrame,
    benchmark_name: str,
    out_dir: str,
    global_frequency: float,
    profile_frequency: float,
    max_cycles: float,
):
    """
    plot the histogram of latency distribution.
    """
    if not os.path.exists(out_dir):
        os.makedirs(out_dir)

    if df.empty:
        print("DataFrame 为空，无法绘图。")
        return

    cycles_per_snapshot = global_frequency / profile_frequency
    cycle_values = df["Epoch"] * cycles_per_snapshot
    keep_mask = cycle_values <= max_cycles
    df = df[keep_mask].reset_index(drop=True)
    cycle_values = cycle_values[keep_mask].reset_index(drop=True)

    if df.empty:
        print(f"{benchmark_name}: no data within {max_cycles:g} cycles, skip plotting.")
        return

    x_values = cycle_values / 10_000
    x_max = max_cycles / 10_000
    x_padding = max(x_max * 0.02, cycles_per_snapshot / 10_000 * 0.5)
    x_ticks = [0, x_max / 3, x_max * 2 / 3, x_max]

    # Set Arial font family
    plt.rcParams["font.family"] = "Arial"
    # For macOS, you might need to explicitly set the font file
    plt.rcParams["font.sans-serif"] = ["Arial"]
    plt.rcParams["mathtext.fontset"] = "custom"
    plt.rcParams["mathtext.rm"] = "Arial"
    plt.rcParams["mathtext.it"] = "Arial:italic"
    plt.rcParams["mathtext.bf"] = "Arial:bold"

    plt.figure(figsize=(4.25, 2.65), dpi=300)
    
    # 绘制两组数据
    plt.plot(x_values, df['Walk Occupancy'], label='MSHR for Walk', marker='.', linestyle='-', color="#313A5B")
    plt.plot(x_values, df['Normal Occupancy'], label='MSHR for Kernel', marker='.', linestyle='--', color="#7AB656")
    plt.plot(x_values, df['Reserve'], label='Reserved MSHR', marker='.', linestyle='-', color="red", linewidth=0.5, markersize=2)
    
    # 添加图表信息
    # plt.xlabel(r'Cycles ($\times 10^4$)', fontsize=28, fontweight='bold')
    plt.ylabel('Occupancy (%)', fontsize=24, fontweight='bold')
    plt.xlim(-x_padding, x_max + x_padding)
    ax = plt.gca()
    plt.xticks(
        x_ticks,
        [f"{tick:g}" for tick in x_ticks],
        fontsize=28,
        fontweight="bold",
    )
    plt.ylim(0, 100)
    plt.yticks(
        [0, 25, 50, 75, 100],
        ["0", "", "50", "", "100"],
        fontsize=28,
        fontweight="bold",
    )
    # plt.legend()
    plt.grid(True, linestyle=':', alpha=0.6) # 显示网格

    for spine in ax.spines.values():
        spine.set_linewidth(1.75)  # 设置边框宽度为 2.5，可根据需要调整
    
    # 显示图形
    plt.tight_layout(pad=0.15)
    output_file = os.path.join(
        out_dir, f"{benchmark_name}_amr"
    )
    plt.savefig(output_file + ".png", dpi=300, bbox_inches='tight', pad_inches=0.03)
    plt.savefig(output_file + ".pdf", dpi=300, bbox_inches='tight', pad_inches=0.03)
    print(f"Plot saved to {output_file}")

    # 6. 单独生成一个图例文件
    plt.figure(figsize=(10, 1), dpi=300)
    legend_elements = [
        Line2D([0], [0], label='MSHR for Walk Occupancy', marker='.', linestyle='-', color="#313A5B", lw=2),
        Line2D([0], [0], label='MSHR for Kernel Occupancy', marker='.', linestyle='--', color="#7AB656", lw=2),
        Line2D([0], [0], label='Reserved MSHR', marker='.', linestyle='-', color="red", lw=2),
    ]
    plt.legend(
        handles=legend_elements,
        ncol=3,
        loc="center",
        frameon=True,
        fancybox=True,
        framealpha=0.7,
        prop={"weight": "bold", "size": 18},
    )
    plt.axis('off')
    legend_output_file = os.path.join(out_dir, "amr_legend")
    plt.savefig(legend_output_file + ".png", dpi=300, bbox_inches='tight')
    plt.savefig(legend_output_file + ".pdf", dpi=300, bbox_inches='tight')
    plt.close("all")

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
    parser.add_argument(
        "--inputDir",
        default="../../final_final_data/250KHz",
        type=str,
        help="Directory path containing benchmark trace files.",
    )
    parser.add_argument(
        "--globalFrequency",
        default=parse_frequency("1GHz"),
        type=parse_frequency,
        help="Global frequency in Hz, or with suffix Hz/KHz/MHz/GHz. Default: 1GHz.",
    )
    parser.add_argument(
        "--profileFrequency",
        default=parse_frequency("250KHz"),
        type=parse_frequency,
        help="Profile snapshot frequency in Hz, or with suffix Hz/KHz/MHz/GHz. Default: 250KHz.",
    )
    parser.add_argument(
        "--maxCycles",
        default=400_000,
        type=float,
        help="Only plot snapshots up to this many cycles. Default: 400000.",
    )

    args = parser.parse_args()

    for benchmark in get_benchmarks():
        result = read_data_to_dataframe(
            benchmark_name=benchmark,
            input_dir=args.inputDir,
        )
        if result.empty:
            continue
        # Only keep GPC0
        result = result[result["GPC"] == 0].reset_index(drop=True)
        print(result)
        # 2. 调用绘图函数
        plot_data(
            result,
            benchmark,
            args.outDir,
            args.globalFrequency,
            args.profileFrequency,
            args.maxCycles,
        )
