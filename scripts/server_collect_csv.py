#!/usr/bin/env python3
import os
import shutil
import zipfile
import argparse

benchmarks = [
    "convolution2d",
    "fastwalshtransform",
    "gups",
    "jacobi1d",
    "jacobi2d",
    "kmeans",
    "matrixtranspose",
    "mis",
    "pagerank",
    "shoc-reduction",
    "simpleconvolution",
    "spmv",
    "stencil2d",
    "syr2k",
    "syrk",
]
# =============================================


def collect_and_zip(base_dir, zips_dir, zip_prefix):
    csv_dir = os.path.join(base_dir, zip_prefix)
    os.makedirs(csv_dir, exist_ok=True)

    for bench in benchmarks:
        metrics_path = os.path.join(base_dir, bench, "metrics.csv")
        if os.path.exists(metrics_path):
            new_name = f"{bench}.csv"
            target_path = os.path.join(csv_dir, new_name)
            shutil.copy(metrics_path, target_path)
            print(f"[OK] Copied: {metrics_path} → {target_path}")
        else:
            print(f"[Skip] {bench}: metrics.csv not found.")

    shutil.copy(
        os.path.join(base_dir, "run_summary.log"),
        os.path.join(csv_dir, "run_summary.log"),
    )

    zip_name = f"{zip_prefix}.zip"
    zip_path = os.path.join(base_dir, zip_name)

    with zipfile.ZipFile(zip_path, "w", zipfile.ZIP_DEFLATED) as zipf:
        for root, _, files in os.walk(csv_dir):
            for file in files:
                abs_path = os.path.join(root, file)
                rel_path = os.path.relpath(abs_path, base_dir)
                zipf.write(abs_path, rel_path)
    print(f"\n✅ Created zip: {zip_path}")

    dest_zip = os.path.join(zips_dir, zip_name)
    shutil.move(zip_path, dest_zip)
    print(f"✅ Moved to: {dest_zip}")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(
        description="Collect metrics.csv from benchmarks and zip them."
    )
    parser.add_argument(
        "--zip_prefix", help="Prefix name for the output zip file (without .zip)"
    )
    parser.add_argument(
        "--input", required=True, help="Input directory containing benchmark folders"
    )
    parser.add_argument(
        "--output", required=True, help="Output directory for the zip file"
    )
    args = parser.parse_args()

    collect_and_zip(args.input, args.output, args.zip_prefix)
