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
    file_path = os.path.join(input_dir, f"{benchmark_name}.csv")
    if not os.path.exists(file_path):
        print(f"Performance data file {file_path} does not exist.")
        return performance_data
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
            print(f"Warning: kernel time (force stop) for {benchmark_name} is {performance_data}.")
            break
    return performance_data


def plot_normalized_time(
    baseline_2gpc: pd.DataFrame,
    mpw_2gpc: pd.DataFrame,
    ngat_2gpc: pd.DataFrame,
    baseline_4gpc: pd.DataFrame,
    mpw_4gpc: pd.DataFrame,
    ngat_4gpc: pd.DataFrame,
    baseline_8gpc: pd.DataFrame,
    mpw_8gpc: pd.DataFrame,
    ngat_8gpc: pd.DataFrame,
    out_dir: str,
) -> None:
    if not os.path.exists(out_dir):
        os.makedirs(out_dir)

    plt.rcParams["font.family"] = "Arial"
    plt.rcParams["font.sans-serif"] = ["Arial"]
    plt.rcParams["mathtext.fontset"] = "custom"
    plt.rcParams["mathtext.rm"] = "Arial"
    plt.rcParams["mathtext.it"] = "Arial:italic"
    plt.rcParams["mathtext.bf"] = "Arial:bold"
    plt.rcParams["hatch.linewidth"] = 2.0

    plt.figure(figsize=(20, 5), dpi=300)

    benchmarks = get_high_mpki_benchmarks()

    bar_width = 0.1
    r1 = np.arange(len(benchmarks) + 1) * (6 * bar_width + 0.2)
    r2 = [x + bar_width for x in r1]
    r3 = [x + bar_width for x in r2]
    r4 = [x + bar_width for x in r3]
    r5 = [x + bar_width for x in r4]
    r6 = [x + bar_width for x in r5]

    # Normalize: baseline_8gpc as reference (=1.0)
    # mpw_2gpc["Data"] = [
    #     (baseline_8gpc["Data"][i] / mpw_2gpc["Data"][i]
    #      if mpw_2gpc["Data"][i] != 0 and baseline_8gpc["Data"][i] != 0 else 0)
    #     for i in range(len(benchmarks))
    # ]
    # ngat_2gpc["Data"] = [
    #     (baseline_8gpc["Data"][i] / ngat_2gpc["Data"][i]
    #      if ngat_2gpc["Data"][i] != 0 and baseline_8gpc["Data"][i] != 0 else 0)
    #     for i in range(len(benchmarks))
    # ]
    # baseline_2gpc["Data"] = [
    #     (baseline_8gpc["Data"][i] / baseline_2gpc["Data"][i]
    #      if baseline_2gpc["Data"][i] != 0 and baseline_8gpc["Data"][i] != 0 else 0)
    #     for i in range(len(benchmarks))
    # ]
    # mpw_4gpc["Data"] = [
    #     (baseline_8gpc["Data"][i] / mpw_4gpc["Data"][i]
    #      if mpw_4gpc["Data"][i] != 0 and baseline_8gpc["Data"][i] != 0 else 0)
    #     for i in range(len(benchmarks))
    # ]
    # ngat_4gpc["Data"] = [
    #     (baseline_8gpc["Data"][i] / ngat_4gpc["Data"][i]
    #      if ngat_4gpc["Data"][i] != 0 and baseline_8gpc["Data"][i] != 0 else 0)
    #     for i in range(len(benchmarks))
    # ]
    # baseline_4gpc["Data"] = [
    #     (baseline_8gpc["Data"][i] / baseline_4gpc["Data"][i]
    #      if baseline_4gpc["Data"][i] != 0 and baseline_8gpc["Data"][i] != 0 else 0)
    #     for i in range(len(benchmarks))
    # ]
    # mpw_8gpc["Data"] = [
    #     (baseline_8gpc["Data"][i] / mpw_8gpc["Data"][i]
    #      if mpw_8gpc["Data"][i] != 0 and baseline_8gpc["Data"][i] != 0 else 0)
    #     for i in range(len(benchmarks))
    # ]
    # ngat_8gpc["Data"] = [
    #     (baseline_8gpc["Data"][i] / ngat_8gpc["Data"][i]
    #      if ngat_8gpc["Data"][i] != 0 and baseline_8gpc["Data"][i] != 0 else 0)
    #     for i in range(len(benchmarks))
    # ]
    # baseline_8gpc["Data"] = [1.0 for _ in range(len(benchmarks))]
    ngat_2gpc["Data"] = [
        (baseline_2gpc["Data"][i] / ngat_2gpc["Data"][i]
         if ngat_2gpc["Data"][i] != 0 and baseline_2gpc["Data"][i] != 0 else 0)
        for i in range(len(benchmarks))
    ]
    baseline_2gpc["Data"] = [
        (baseline_2gpc["Data"][i] / baseline_2gpc["Data"][i]
         if baseline_2gpc["Data"][i] != 0 and baseline_2gpc["Data"][i] != 0 else 0)
        for i in range(len(benchmarks))
    ]
    ngat_4gpc["Data"] = [
        (baseline_4gpc["Data"][i] / ngat_4gpc["Data"][i]
         if ngat_4gpc["Data"][i] != 0 and baseline_4gpc["Data"][i] != 0 else 0)
        for i in range(len(benchmarks))
    ]
    baseline_4gpc["Data"] = [
        (baseline_4gpc["Data"][i] / baseline_4gpc["Data"][i]
         if baseline_4gpc["Data"][i] != 0 and baseline_4gpc["Data"][i] != 0 else 0)
        for i in range(len(benchmarks))
    ]
    ngat_8gpc["Data"] = [
        (baseline_8gpc["Data"][i] / ngat_8gpc["Data"][i]
         if ngat_8gpc["Data"][i] != 0 and baseline_8gpc["Data"][i] != 0 else 0)
        for i in range(len(benchmarks))
    ]
    baseline_8gpc["Data"] = [1.0 for _ in range(len(benchmarks))]

    # Append harmonic mean as "Ave."
    def append_ave(df):
        return pd.concat(
            [df, pd.DataFrame({"Benchmark": ["Ave."], "Data": [harmonic_mean(df["Data"])]})],
            ignore_index=True,
        )

    baseline_2gpc = append_ave(baseline_2gpc)
    mpw_2gpc      = append_ave(mpw_2gpc)
    ngat_2gpc     = append_ave(ngat_2gpc)
    baseline_4gpc = append_ave(baseline_4gpc)
    mpw_4gpc      = append_ave(mpw_4gpc)
    ngat_4gpc     = append_ave(ngat_4gpc)
    baseline_8gpc = append_ave(baseline_8gpc)
    mpw_8gpc      = append_ave(mpw_8gpc)
    ngat_8gpc     = append_ave(ngat_8gpc)

    # Colors per GPC tier
    color_2gpc = "#C3D9F1"
    color_4gpc = "#5D73A1"
    color_8gpc = "#313A5B"

    bar_baseline_2gpc = plt.bar(r1, baseline_2gpc["Data"], width=bar_width,
                                color=color_2gpc, edgecolor="black", linewidth=1.5)
    bar_baseline_4gpc = plt.bar(r3, baseline_4gpc["Data"], width=bar_width,
                                color=color_4gpc, edgecolor="black", linewidth=1.5)
    bar_baseline_8gpc = plt.bar(r5, baseline_8gpc["Data"], width=bar_width,
                                color=color_8gpc, edgecolor="black", linewidth=1.5)

    bar_ngat_2gpc = plt.bar(r2, ngat_2gpc["Data"], width=bar_width,
                            color=color_2gpc, edgecolor="black", linewidth=1.5, hatch="xx")
    bar_ngat_4gpc = plt.bar(r4, ngat_4gpc["Data"], width=bar_width,
                            color=color_4gpc, edgecolor="black", linewidth=1.5, hatch="xx")
    bar_ngat_8gpc = plt.bar(r6, ngat_8gpc["Data"], width=bar_width,
                            color=color_8gpc, edgecolor="black", linewidth=1.5, hatch="xx")

    plt.xlim(min(r1) - bar_width, max(r6) + bar_width)
    plt.xticks(
        [r + 2.5 * bar_width for r in r1],
        [get_short_name(benchmarks[i]) for i in range(len(benchmarks))] + ["Ave."],
        fontsize=28, fontweight="bold",
    )
    plt.ylabel("Speedup", fontsize=28, fontweight="bold")
    plt.yticks(np.arange(0, 8.1, 2), fontsize=28, fontweight="bold")
    plt.ylim(0, 8)

    legend_gpc = [
        Patch(facecolor=color_2gpc, edgecolor="black", linewidth=1.5, label="2 GPC"),
        Patch(facecolor=color_4gpc, edgecolor="black", linewidth=1.5, label="4 GPC"),
        Patch(facecolor=color_8gpc, edgecolor="black", linewidth=1.5, label="8 GPC"),
    ]
    legend_type = [
        Patch(facecolor="white", edgecolor="black", linewidth=1.5, label="Baseline"),
        Patch(facecolor="white", edgecolor="black", linewidth=1.5, hatch="xx", label="ngAT"),
    ]

    leg1 = plt.legend(
        handles=legend_gpc,
        loc="upper left", bbox_to_anchor=(0.1, 1.0),
        bbox_transform=plt.gcf().transFigure,
        frameon=True, fancybox=True, framealpha=0.7,
        ncol=3, prop={"weight": "bold", "size": 24},
        title_fontproperties={"weight": "bold", "size": 22},
    )
    leg2 = plt.legend(
        handles=legend_type,
        loc="upper right", bbox_to_anchor=(0.9, 1.0),
        bbox_transform=plt.gcf().transFigure,
        frameon=True, fancybox=True, framealpha=0.7,
        ncol=2, prop={"weight": "bold", "size": 24},
        title_fontproperties={"weight": "bold", "size": 22},
    )
    plt.gca().add_artist(leg1)

    plt.tight_layout(rect=[0, 0, 1, 0.95])
    plt.grid(axis="y", alpha=0.3)
    plt.axhline(y=1, color="red", linewidth=0.8, linestyle="--")

    ax = plt.gca()
    for spine in ax.spines.values():
        spine.set_linewidth(1.75)

    output_file = os.path.join(out_dir, "sensitivity_gpc")
    plt.savefig(output_file + ".png")
    plt.savefig(output_file + ".pdf")
    print(f"Plot saved to {output_file}")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="Parse csv file.")
    parser.add_argument("--outDir", required=True, type=str,
                        help="Directory path to save the output plots.")
    args = parser.parse_args()

    # Cleaner loop — rebuild from scratch properly
    baseline_2gpc = pd.DataFrame(columns=["Benchmark", "Data"])
    mpw_2gpc     = pd.DataFrame(columns=["Benchmark", "Data"])
    ngat_2gpc = pd.DataFrame(columns=["Benchmark", "Data"])
    baseline_4gpc     = pd.DataFrame(columns=["Benchmark", "Data"])
    mpw_4gpc = pd.DataFrame(columns=["Benchmark", "Data"])
    ngat_4gpc     = pd.DataFrame(columns=["Benchmark", "Data"])
    baseline_8gpc = pd.DataFrame(columns=["Benchmark", "Data"])
    mpw_8gpc     = pd.DataFrame(columns=["Benchmark", "Data"])
    ngat_8gpc     = pd.DataFrame(columns=["Benchmark", "Data"])

    for benchmark in get_high_mpki_benchmarks():
        def append_row(df, benchmark, input_dir):
            perf_data = collect_performance_data(benchmark_name=benchmark, input_dir=input_dir)
            return pd.concat(
                [df, pd.DataFrame({"Benchmark": [benchmark], "Data": [perf_data]})],
                ignore_index=True,
            )

        baseline_2gpc = append_row(baseline_2gpc, benchmark, "../../final_data/final_baseline_2GPC")
        mpw_2gpc    = append_row(mpw_2gpc,     benchmark, "../../final_data/final_mpw_2GPC")
        ngat_2gpc     = append_row(ngat_2gpc,     benchmark, "../../final_data/final_ngat_2GPC")
        baseline_4gpc    = append_row(baseline_4gpc,     benchmark, "../../final_data/final_baseline_4GPC")
        mpw_4gpc     = append_row(mpw_4gpc,     benchmark, "../../final_data/final_mpw_4GPC")
        ngat_4gpc     = append_row(ngat_4gpc,     benchmark, "../../final_data/final_ngat_4GPC")
        baseline_8gpc    = append_row(baseline_8gpc,     benchmark, "../../final_data/final_baseline")
        mpw_8gpc     = append_row(mpw_8gpc,     benchmark, "../../final_data/final_mpw")
        ngat_8gpc     = append_row(ngat_8gpc,     benchmark, "../../final_data/final_ngat_adaptive")

    plot_normalized_time(
        baseline_2gpc=baseline_2gpc.copy(),
        mpw_2gpc=mpw_2gpc.copy(),
        ngat_2gpc=ngat_2gpc.copy(),
        baseline_4gpc=baseline_4gpc.copy(),
        mpw_4gpc=mpw_4gpc.copy(),
        ngat_4gpc=ngat_4gpc.copy(),
        baseline_8gpc=baseline_8gpc.copy(),
        mpw_8gpc=mpw_8gpc.copy(),
        ngat_8gpc=ngat_8gpc.copy(),
        out_dir=args.outDir,
    )