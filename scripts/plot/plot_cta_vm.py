import os
import json
import argparse
import numpy as np
import matplotlib.pyplot as plt
from benchmark import benchmarks


def compute_cta_page_count(cta_pages: dict) -> dict:
    cta_indices = sorted([int(k) for k in cta_pages.keys()])
    return {cta: len(cta_pages[str(cta)]) for cta in cta_indices}


# ------------------------------------------------------------
# 绘图主函数
# ------------------------------------------------------------
def plot_cta_pages(input_dir: str, benchmark: str, out_dir: str):
    json_path = os.path.join(input_dir, f"{benchmark}.cta.json")
    if not os.path.exists(json_path):
        raise FileNotFoundError(f"JSON file not found: {json_path}")

    # Load JSON
    with open(json_path, "r") as f:
        cta_pages = json.load(f)

    # Convert CTA index to sorted integers
    cta_indices = sorted([int(k) for k in cta_pages.keys()])

    # Extract Y data: list of lists
    pages_list = [cta_pages[str(cta)] for cta in cta_indices]

    # ------------------------------------------------------------
    # Matplotlib global styling
    # ------------------------------------------------------------
    plt.rcParams["font.family"] = "Arial"
    plt.rcParams["font.sans-serif"] = ["Arial"]
    plt.rcParams["mathtext.fontset"] = "custom"
    plt.rcParams["mathtext.rm"] = "Arial"
    plt.rcParams["mathtext.it"] = "Arial:italic"
    plt.rcParams["mathtext.bf"] = "Arial:bold"

    plt.figure(figsize=(20, 5), dpi=300)

    # ------------------------------------------------------------
    # 绘制 scatter，每个 CTA 一个竖直散点列
    # ------------------------------------------------------------

    for i, cta in enumerate(cta_indices):
        pages = pages_list[i]
        x = np.full(len(pages), i)  # same CTA index

        plt.scatter(
            x,
            pages,
            color="#cce5d8",
            s=2,
            edgecolors="black",
            linewidths=0.6,
            label="_nolegend_",
        )

    # ------------------------------------------------------------
    # Axis setup
    # ------------------------------------------------------------
    max_ticks = 32
    num_cta = len(cta_indices)

    if num_cta <= max_ticks:
        tick_positions = np.arange(num_cta)
        tick_labels = [str(cta) for cta in cta_indices]
    else:
        step = max(1, num_cta // max_ticks)
        tick_positions = np.arange(0, num_cta, step)
        tick_labels = [str(cta_indices[i]) for i in tick_positions]

    plt.xticks(
        tick_positions,
        tick_labels,
        fontsize=22,
        fontweight="bold",
        rotation=45,
    )
    plt.ylabel("Page Number", fontsize=22, fontweight="bold")
    plt.yticks(fontsize=22, fontweight="bold")

    plt.grid(axis="y", alpha=0.3)

    # Border thickness
    ax = plt.gca()
    for spine in ax.spines.values():
        spine.set_linewidth(1.75)

    plt.tight_layout()

    # ------------------------------------------------------------
    # Save figure
    # ------------------------------------------------------------
    if not os.path.exists(out_dir):
        os.makedirs(out_dir)

    output_file = os.path.join(out_dir, f"{benchmark}_cta_pages")
    plt.savefig(output_file + ".png")
    plt.savefig(output_file + ".pdf")

    print(f"Figure saved to {output_file}.png and .pdf")


def plot_cta_page_count_bar(input_dir: str, benchmark: str, out_dir: str):
    # ------------------------------------------------------------
    # Matplotlib global styling
    # ------------------------------------------------------------
    plt.rcParams["font.family"] = "Arial"
    plt.rcParams["font.sans-serif"] = ["Arial"]
    plt.rcParams["mathtext.fontset"] = "custom"
    plt.rcParams["mathtext.rm"] = "Arial"
    plt.rcParams["mathtext.it"] = "Arial:italic"
    plt.rcParams["mathtext.bf"] = "Arial:bold"

    json_path = os.path.join(input_dir, f"{benchmark}.cta.json")
    if not os.path.exists(json_path):
        raise FileNotFoundError(f"JSON file not found: {json_path}")

    # Load JSON
    with open(json_path, "r") as f:
        cta_pages = json.load(f)

    page_count_dict = compute_cta_page_count(cta_pages)

    cta_indices = list(page_count_dict.keys())
    page_counts = list(page_count_dict.values())

    plt.figure(figsize=(20, 5), dpi=300)

    bar_color = "#6ba78b"
    plt.bar(
        range(len(cta_indices)),
        page_counts,
        width=0.8,
        color=bar_color,
        # edgecolor="black",
        # linewidth=1.2,
    )

    # X ticks sparse
    max_ticks = 32
    num_cta = len(cta_indices)
    if num_cta <= max_ticks:
        tick_positions = np.arange(num_cta)
        tick_labels = [str(i) for i in cta_indices]
    else:
        step = max(1, num_cta // max_ticks)
        tick_positions = np.arange(0, num_cta, step)
        tick_labels = [str(cta_indices[i]) for i in tick_positions]

    plt.xticks(
        tick_positions,
        tick_labels,
        fontsize=22,
        fontweight="bold",
        rotation=45,
    )

    plt.ylabel("Page Count", fontsize=22, fontweight="bold")
    plt.yticks(fontsize=22, fontweight="bold")
    plt.grid(axis="y", alpha=0.3)

    ax = plt.gca()
    for spine in ax.spines.values():
        spine.set_linewidth(1.75)

    plt.tight_layout()

    # Save figure
    os.makedirs(out_dir, exist_ok=True)
    out_path = os.path.join(out_dir, f"{benchmark}_cta_page_count")
    plt.savefig(out_path + ".png")
    plt.savefig(out_path + ".pdf")
    plt.close()

    print(f"[OK] Saved page-count bar figure: {out_path}")


# ------------------------------------------------------------
# Argument parser
# ------------------------------------------------------------
def main():
    parser = argparse.ArgumentParser(description="Plot CTA page distribution")
    parser.add_argument(
        "--input", required=True, help="Input directory containing JSON"
    )
    parser.add_argument("--output", required=True, help="Output directory")
    args = parser.parse_args()

    for benchmark in benchmarks:
        try:
            plot_cta_pages(args.input, benchmark, args.output)
            plot_cta_page_count_bar(args.input, benchmark, args.output)
        except FileNotFoundError as e:
            print(e)


if __name__ == "__main__":
    main()
