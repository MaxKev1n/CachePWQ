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


def collect_performance_data(
    benchmark_name: str,
    input_dir: str,
) -> list:
    """
    Collects performance data from the specified input directory.

    Args:
        benchmark_name (str): The name of the benchmark.
        input_dir (str): The directory containing performance data files.

    Returns:
        list: A list of dictionaries containing performance data.
    """
    performance_data = [0, 0]

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

        if "BookSimNocTraffic" in what and "L1ToL2NoC" in where:
            split_what = what.split(" ")
            src_node = int(split_what[2].split(":")[0])
            dst_node = int(split_what[2].split(":")[-1])

            if src_node < 192:
                performance_data[0] += value
            elif src_node == 192:
                performance_data[1] += value

    return performance_data


def plot_transaction(
    data: pd.DataFrame,
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

    bar_width = 0.3
    r1 = np.arange(len(benchmarks) + 1)

    trans_sum = [data["data"][i] + data["page"][i] for i in range(len(benchmarks))]

    data_rate = [
        data["data"][i] / trans_sum[i] if trans_sum[i] != 0 else 0
        for i in range(len(benchmarks))
    ]
    page_rate = [
        data["page"][i] / trans_sum[i] if trans_sum[i] != 0 else 0
        for i in range(len(benchmarks))
    ]

    data_gmean = geometric_mean(pd.Series(data["data"]))
    page_gmean = geometric_mean(pd.Series(data["page"]))

    data_rate.append(
        data_gmean / (data_gmean + page_gmean) if (data_gmean + page_gmean) != 0 else 0
    )
    page_rate.append(
        page_gmean / (data_gmean + page_gmean) if (data_gmean + page_gmean) != 0 else 0
    )

    data_rate = [r * 100 for r in data_rate]
    page_rate = [r * 100 for r in page_rate]

    bar1 = plt.bar(
        r1,
        data_rate,
        width=bar_width,
        label="Data",
        color="#fcfdf7",
        edgecolor="black",
        linewidth=1.5,
    )
    bar2 = plt.bar(
        r1,
        page_rate,
        width=bar_width,
        bottom=data_rate,
        label="Page",
        color="#8fbcd4",
        edgecolor="black",
        linewidth=1.5,
    )

    plt.xlim(min(r1) - bar_width, max(r1) + bar_width)
    plt.xticks(
        [r for r in r1],
        [get_short_name(benchmarks[i]) for i in range(len(benchmarks))] + ["GMean"],
        fontsize=22,
        fontweight="bold",
    )
    plt.ylabel("Fraction of L2 Access (%)", fontsize=22, fontweight="bold")
    plt.yticks(
        np.arange(0, 101, 25),
        fontsize=22,
        fontweight="bold",
    )
    plt.ylim(0, 100)
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
    # plt.axhline(y=1, color="red", linewidth=0.8, linestyle="--")

    ax = plt.gca()

    # 设置图的边框加粗
    for spine in ax.spines.values():
        spine.set_linewidth(1.75)  # 设置边框宽度为 2.5，可根据需要调整

    output_file = os.path.join(out_dir, "MemorySide_transaction_Rate")
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

    data = pd.DataFrame(columns=["Benchmark", "data", "page"])

    for benchmark in get_benchmarks():
        perf_data = collect_performance_data(
            benchmark_name=benchmark,
            input_dir="../../data/MemorySide-2NoC-Booksim",
        )
        data = pd.concat(
            [
                data,
                pd.DataFrame(
                    {
                        "Benchmark": [benchmark],
                        "data": [perf_data[0]],
                        "page": [perf_data[1]],
                    }
                ),
            ],
            ignore_index=True,
        )

    plot_transaction(
        data,
        args.outDir,
    )
