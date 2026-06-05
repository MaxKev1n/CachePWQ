import argparse
import os
import pandas as pd
import numpy as np
import matplotlib.pyplot as plt
from benchmark import get_benchmarks, get_short_name

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


def collect_data(benchmark_name: str, input_dir: str) -> pd.DataFrame:
    """
    Collects performance data from the specified input directory.

    Args:
        benchmark_name (str): The name of the benchmark.
        input_dir (str): The directory containing performance data files.

    Returns:
        list: A list of dictionaries containing performance data.
    """
    data = pd.DataFrame(columns=["cycles", "numbers", "frequency"])

    file_path = os.path.join(input_dir, f"{benchmark_name}.csv")

    if not os.path.exists(file_path):
        print(f"Performance data file {file_path} does not exist.")

        return data

    # read the CSV file and collect performance data
    df = pd.read_csv(file_path)

    for _, row in df.iterrows():
        if "BaselineMMU" not in row.iloc[1] or "req_latency_histogram_" not in row.iloc[2]:
            continue

        cycle = int(row.iloc[2].split("_")[-1])
        numbers = int(row.iloc[3])

        if cycle in data["cycles"].values:
            data.loc[data["cycles"] == cycle, "numbers"] += numbers
        else:
            data = pd.concat(
            [
                data,
                pd.DataFrame(
                    {
                        "cycles": [cycle],
                        "numbers": [numbers],
                    }
                ),
            ],
            ignore_index=True,
        )

    total_numbers = data["numbers"].sum()
    
    data["frequency"] = data["numbers"] / total_numbers * 100

    # sort the data by cycles
    data = data.sort_values(by="cycles").reset_index(drop=True)

    # fill the missing cycles with 0 frequency
    largest_cycle = data["cycles"].max()
    all_cycles = pd.DataFrame({"cycles": np.arange(160, largest_cycle + 1, 20)})
    data = pd.merge(all_cycles, data, on="cycles", how="left").fillna(0)

    return data

def plot_latency_histogram(
    data: pd.DataFrame,
    out_dir: str,
):
    """
    plot the histogram of latency distribution.
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

    plt.figure(figsize=(8, 4), dpi=300)

    bar_width = 0.05

    cycles = data["cycles"].tolist()[0:33]
    frequency = data["frequency"].tolist()[0:33]

    r1 = np.arange(len(cycles)) * (1 * bar_width)  # Adjust the spacing between bars
    
    bar = plt.bar(
        r1, 
        frequency, 
        width=bar_width, 
        color='#C3D9F1', 
        edgecolor='black', 
        linewidth=1,
    )

    plt.xlabel('L2 access latency', fontsize=24, fontweight="bold")
    plt.ylabel('Frequency (%)', fontsize=24, fontweight="bold")

    plt.xlim(min(r1) - bar_width, max(r1) + bar_width)
    plt.xticks(
        np.arange(0, 1.61, 0.1),
        [cycles[i] for i in range(0, 33, 2)],
        fontsize=24,
        fontweight="bold",
        rotation=90,
    )
    plt.ylim(0, 18)
    plt.yticks(
        np.arange(0, 19, 3),
        fontsize=24,
        fontweight="bold",
    )
    plt.grid(axis="y", alpha=0.3)
    plt.tight_layout()

    ax = plt.gca()

    for spine in ax.spines.values():
        spine.set_linewidth(1.75)  # 设置边框宽度为 2.5，可根据需要调整
    
    output_file = os.path.join(
        out_dir, f"{benchmark}_baseline_numa_latency_distribution"
    )
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

    for benchmark in get_benchmarks():
        data = collect_data(
            benchmark_name=benchmark,
            input_dir="../../final_final_data/baseline",
        )

        try :
            plot_latency_histogram(
                data=data,
                out_dir=args.outDir,
            )
        except Exception as e:
            print(f"Error plotting latency histogram for {benchmark}: {e}")
            
