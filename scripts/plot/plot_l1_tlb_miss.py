import argparse
import os
import pandas as pd
import numpy as np
import matplotlib.pyplot as plt
from benchmark import get_benchmarks, get_short_name


def geometric_mean(df: pd.DataFrame) -> float:
    """
    Calculate the geometric mean of a list of numbers.

    Args:
        data (list): A list of numerical values.

    Returns:
        float: The geometric mean of the input data.
    """
    data = df.tolist()
    if not data or any(x <= 0 for x in data):
        return 0.0

    product = np.prod(data)
    return product ** (1 / len(data))


def collect_performance_csv(
    file_path: str,
) -> float:
    """
    Collects performance data from the specified input directory.

    Args:
        benchmark_name (str): The name of the benchmark.
        input_dir (str): The directory containing performance data files.

    Returns:
        list: A list of dictionaries containing performance data.
    """
    performance_data = [0, 0, 0]

    if not os.path.exists(file_path):
        print(f"Performance data file {file_path} does not exist.")

        return performance_data

    # read the CSV file and collect performance data
    df = pd.read_csv(file_path)

    for _, row in df.iterrows():
        where = row.iloc[1]
        what = row.iloc[2]
        value = row.iloc[3]

        if "TLB" in where and "L2" in where:
            if " tlb-hit" == what:
                performance_data[0] += value
            elif " tlb-miss" == what:
                performance_data[1] += value
            elif " tlb-mshr-hit" == what:
                performance_data[2] += value

    return performance_data


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


def plot_normalized_time(
    baseline: pd.DataFrame,
    Opt1: pd.DataFrame,
    Opt2: pd.DataFrame,
    Opt3: pd.DataFrame,
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

    benchmarks = get_benchmarks()

    bar_width = 0.4
    r1 = np.arange(len(benchmarks) + 1) * (4 * bar_width + 0.2)
    r2 = [x + bar_width for x in r1]
    r3 = [x + bar_width for x in r2]
    r4 = [x + bar_width for x in r3]

    # Geometric mean
    baseline = pd.concat(
        [
            baseline,
            pd.DataFrame(
                {
                    "Benchmark": ["Geometric Mean"],
                    "Data": [geometric_mean(baseline["Data"])],
                }
            ),
        ],
        ignore_index=True,
    )
    Opt1 = pd.concat(
        [
            Opt1,
            pd.DataFrame(
                {
                    "Benchmark": ["Geometric Mean"],
                    "Data": [geometric_mean(Opt1["Data"])],
                }
            ),
        ],
        ignore_index=True,
    )
    Opt2 = pd.concat(
        [
            Opt2,
            pd.DataFrame(
                {
                    "Benchmark": ["Geometric Mean"],
                    "Data": [geometric_mean(Opt2["Data"])],
                }
            ),
        ],
        ignore_index=True,
    )
    Opt3 = pd.concat(
        [
            Opt3,
            pd.DataFrame(
                {
                    "Benchmark": ["Geometric Mean"],
                    "Data": [geometric_mean(Opt3["Data"])],
                }
            ),
        ],
        ignore_index=True,
    )

    # Normalize the time
    Opt1["Data"] = [
        (
            baseline["Data"][i] / Opt1["Data"][i]
            if Opt1["Data"][i] != 0 and baseline["Data"][i] != 0
            else 0
        )
        for i in range(len(benchmarks) + 1)
    ]
    Opt2["Data"] = [
        (
            baseline["Data"][i] / Opt2["Data"][i]
            if Opt2["Data"][i] != 0 and baseline["Data"][i] != 0
            else 0
        )
        for i in range(len(benchmarks) + 1)
    ]
    Opt3["Data"] = [
        (
            baseline["Data"][i] / Opt3["Data"][i]
            if Opt3["Data"][i] != 0 and baseline["Data"][i] != 0
            else 0
        )
        for i in range(len(benchmarks) + 1)
    ]

    baseline["Data"] = [1.0 for _ in range(len(benchmarks) + 1)]

    bar1 = plt.bar(
        r1,
        baseline["Data"],
        width=bar_width,
        label="Baseline",
        color="#fcfdf7",
        edgecolor="black",
        linewidth=1.5,
    )
    bar2 = plt.bar(
        r2,
        Opt1["Data"],
        width=bar_width,
        label="SMSide-Baseline MMU",
        color="#cce5d8",
        edgecolor="black",
        linewidth=1.5,
    )
    bar3 = plt.bar(
        r3,
        Opt2["Data"],
        width=bar_width,
        label="SMSide-Baseline MMU-64",
        color="#6ba78b",
        edgecolor="black",
        linewidth=1.5,
    )
    bar4 = plt.bar(
        r4,
        Opt3["Data"],
        width=bar_width,
        label="Memory-Side Ideal MMU",
        color="#3f6b5c",
        edgecolor="black",
        linewidth=1.5,
    )

    # for bar in bar1 + bar2 + bar3 + bar4:
    #     height = bar.get_height()
    #     x = bar.get_x() + bar.get_width() / 2

    #     plt.annotate(
    #         f"{height:.2f}",
    #         xy=(x, height),
    #         xytext=(0, 20),  # 相对偏移 (0,15) 表示向上15pt
    #         textcoords="offset points",
    #         ha="center",
    #         va="bottom",
    #         fontsize=26,
    #         fontweight="bold",
    #         bbox=dict(
    #             facecolor="white",
    #             edgecolor="black",
    #             boxstyle="round,pad=0.1",
    #         ),
    #         # arrowprops=dict(arrowstyle="-", color="red", lw=2),
    #     )

    plt.xlim(min(r1) - bar_width, max(r4) + bar_width)
    plt.xticks(
        [r + 1.5 * bar_width for r in r1],
        [get_short_name(benchmarks[i]) for i in range(len(benchmarks))] + ["GMean"],
        fontsize=22,
        fontweight="bold",
    )
    plt.ylabel("Speedup", fontsize=22, fontweight="bold")
    plt.yticks(
        np.arange(0, 3.1, 0.5),
        fontsize=22,
        fontweight="bold",
    )
    plt.ylim(0, 3)
    plt.legend(
        loc="upper center",
        ncol=5,
        bbox_to_anchor=(0.5, 1),
        bbox_transform=plt.gcf().transFigure,  # 使用图形坐标系
        frameon=True,
        fancybox=True,
        framealpha=0.7,
        prop={"weight": "bold", "size": 22},
    )
    plt.tight_layout(rect=[0, 0, 1, 0.95])
    plt.grid(axis="y", alpha=0.3)
    plt.axhline(y=1, color="red", linewidth=0.8, linestyle="--")

    ax = plt.gca()

    # 设置图的边框加粗
    for spine in ax.spines.values():
        spine.set_linewidth(1.75)  # 设置边框宽度为 2.5，可根据需要调整

    output_file = os.path.join(out_dir, "SMSideMMU_Performance_Comparison")
    plt.savefig(output_file + ".png")
    plt.savefig(output_file + ".pdf")
    print(f"Plot saved to {output_file}")


def single_benchmark(args):
    perf_data = collect_performance_csv(
        file_path=args.csv,
    )
    print(
        perf_data,
        (perf_data[0] + perf_data[2]) / (perf_data[0] + perf_data[1] + perf_data[2]),
        sum(perf_data),
    )


def multi_benchmark(args):
    baseline = pd.DataFrame(
        columns=["Benchmark", "Data"],
    )
    Opt1 = pd.DataFrame(
        columns=["Benchmark", "Data"],
    )
    Opt2 = pd.DataFrame(
        columns=["Benchmark", "Data"],
    )
    Opt3 = pd.DataFrame(
        columns=["Benchmark", "Data"],
    )

    for benchmark in get_benchmarks():
        perf_data = collect_performance_data(
            benchmark_name=benchmark,
            input_dir="../../data/baseline",
        )

        baseline = pd.concat(
            [
                baseline,
                pd.DataFrame(
                    {
                        "Benchmark": [benchmark],
                        "Data": [perf_data],
                    }
                ),
            ],
            ignore_index=True,
        )

        perf_data = collect_performance_data(
            benchmark_name=benchmark,
            input_dir="../../data/SMSide-baselineMMU",
        )

        Opt1 = pd.concat(
            [
                Opt1,
                pd.DataFrame(
                    {
                        "Benchmark": [benchmark],
                        "Data": [perf_data],
                    }
                ),
            ],
            ignore_index=True,
        )

        perf_data = collect_performance_data(
            benchmark_name=benchmark,
            input_dir="../../data/SMSide-baselineMMU-64",
        )

        Opt2 = pd.concat(
            [
                Opt2,
                pd.DataFrame(
                    {
                        "Benchmark": [benchmark],
                        "Data": [perf_data],
                    }
                ),
            ],
            ignore_index=True,
        )

        perf_data = collect_performance_data(
            benchmark_name=benchmark,
            input_dir="../../data/MemorySideIdealMMU",
        )

        Opt3 = pd.concat(
            [
                Opt3,
                pd.DataFrame(
                    {
                        "Benchmark": [benchmark],
                        "Data": [perf_data],
                    }
                ),
            ],
            ignore_index=True,
        )

    plot_normalized_time(
        baseline=baseline.copy(),
        Opt1=Opt1.copy(),
        Opt2=Opt2.copy(),
        Opt3=Opt3.copy(),
        out_dir=args.outDir,
    )


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
        "--mode",
        required=True,
        type=str,
        choices=["single", "multi"],
        help="Benchmark mode: single or multi.",
    )
    parser.add_argument(
        "--csv",
        type=str,
        help="Path to the CSV file for single benchmark mode.",
    )

    args = parser.parse_args()

    if args.mode == "single":
        single_benchmark(args)
    else:
        multi_benchmark(args)
