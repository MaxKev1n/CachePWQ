import argparse
import os
import numpy as np
import pandas as pd
import matplotlib.pyplot as plt

import benchmark


def parse_csv(
    benchmark: str,
    dirPath: str,
) -> None or pd.DataFrame:
    """
    Parse the csv file.
    """

    print(f"Parsing csv for benchmark: {benchmark}")

    csv_file = os.path.join(dirPath, f"{benchmark}.csv")

    df = pd.DataFrame(
        columns=[
            "Benchmark",
            "L1VCacheHit",
            "L1VCacheMiss",
            "L1VCachePTWMiss",
            "L1VCachePTWMSHRHit",
            "L1SCacheHit",
            "L1SCacheMiss",
            "L2CacheHit",
            "L2CacheMiss",
            "L2CachePTWHit",
            "L2CachePTWMiss",
            "L2CachePTWMSHRHit",
            "L1VTLBHit",
            "L1VTLBMiss",
            "L3TLBHit",
            "L3TLBMiss",
            "InstCount",
        ],
    )

    df.at[0, "Benchmark"] = benchmark
    df.at[0, "L1VCacheHit"] = 0
    df.at[0, "L1VCacheMiss"] = 0
    df.at[0, "L1VCachePTWMiss"] = 0
    df.at[0, "L1VCachePTWMSHRHit"] = 0
    df.at[0, "L1SCacheHit"] = 0
    df.at[0, "L1SCacheMiss"] = 0
    df.at[0, "L2CacheHit"] = 0
    df.at[0, "L2CacheMiss"] = 0
    df.at[0, "L2CachePTWHit"] = 0
    df.at[0, "L2CachePTWMiss"] = 0
    df.at[0, "L2CachePTWMSHRHit"] = 0
    df.at[0, "L1VTLBHit"] = 0
    df.at[0, "L1VTLBMiss"] = 0
    df.at[0, "L3TLBHit"] = 0
    df.at[0, "L3TLBMiss"] = 0
    df.at[0, "InstCount"] = 0

    try:
        with open(csv_file, "r") as file:
            lines = file.readlines()

            for line in lines:
                if (
                    "TLB" in line
                    or "L1VCache" in line
                    or "L1SCache" in line
                    or "L2" in line
                ):
                    parts = line.strip().split(", ")

                    where = str(parts[1])
                    what = str(parts[2])
                    value = float(parts[3])

                    if "L1VCache" in where:
                        if what == "read-hit" or what == "write-hit":
                            df.at[0, "L1VCacheHit"] += value
                        elif what == "read-miss" or what == "write-miss":
                            df.at[0, "L1VCacheMiss"] += value
                        elif what == "ptw-miss":
                            df.at[0, "L1VCachePTWMiss"] += value
                        elif what == "ptw-mshr-hit":
                            df.at[0, "L1VCachePTWMSHRHit"] += value
                    elif "L1SCache" in where:
                        if what == "read-hit" or what == "write-hit":
                            df.at[0, "L1SCacheHit"] += value
                        elif what == "read-miss" or what == "write-miss":
                            df.at[0, "L1SCacheMiss"] += value
                    elif "L2" in where and "L3TLB" not in where:
                        if what == "read-hit" or what == "write-hit":
                            df.at[0, "L2CacheHit"] += value
                        elif what == "read-miss" or what == "write-miss":
                            df.at[0, "L2CacheMiss"] += value
                        elif what == "ptw-hit":
                            df.at[0, "L2CachePTWHit"] += value
                        elif what == "ptw-miss":
                            df.at[0, "L2CachePTWMiss"] += value
                        elif what == "ptw-mshr-hit":
                            df.at[0, "L2CachePTWMSHRHit"] += value
                    elif "L1VTLB" in where:
                        if what == "tlb-hit":
                            df.at[0, "L1VTLBHit"] += value
                        elif what == "tlb-miss":
                            df.at[0, "L1VTLBMiss"] += value
                    elif "L3TLB" in where:
                        if what == "tlb-hit":
                            df.at[0, "L3TLBHit"] += value
                        elif what == "tlb-miss":
                            df.at[0, "L3TLBMiss"] += value

                elif "inst_count" in line:
                    parts = line.strip().split(", ")
                    value = float(parts[3])
                    df.at[0, "InstCount"] += value

        return df

    except FileNotFoundError:
        print(f"File not found: {csv_file}")
        return None
    except Exception as e:
        print(f"Error processing {csv_file}: {e}")
        return None

if __name__ == "__main__":
    parser = argparse.ArgumentParser(
        description="Parse MMU trace file and generate mmu shared report."
    )

    parser.add_argument(
        "--dirPath",
        required=True,
        type=str,
        help="Directory path containing the MMU trace file.",
    )

    parser.add_argument(
        "--outDir",
        required=True,
        type=str,
        help="Directory path to save the output plots.",
    )

    args = parser.parse_args()

    df = pd.DataFrame(
        columns=[
            "Benchmark",
            "L1VCacheHit",
            "L1VCacheMiss",
            "L1VCachePTWMiss",
            "L1VCachePTWMSHRHit",
            "L1SCacheHit",
            "L1SCacheMiss",
            "L2CacheHit",
            "L2CacheMiss",
            "L2CachePTWHit",
            "L2CachePTWMiss",
            "L2CachePTWMSHRHit",
            "L1VTLBHit",
            "L1VTLBMiss",
            "L3TLBHit",
            "L3TLBMiss",
            "InstCount",
        ],
    )

    for benchmark in benchmark.get_benchmarks():
        parsed_df = parse_csv(benchmark, args.dirPath)

        if parsed_df is None:
            print(f"Skipping benchmark {benchmark} due to parsing error.")

            df = df._append(
                {
                    "Benchmark": benchmark,
                    "L1VCacheHit": 0,
                    "L1VCacheMiss": 0,
                    "L1VCachePTWMiss": 0,
                    "L1VCachePTWMSHRHit": 0,
                    "L1SCacheHit": 0,
                    "L1SCacheMiss": 0,
                    "L2CacheHit": 0,
                    "L2CacheMiss": 0,
                    "L2CacheHit": 0,
                    "L2CachePTWHit": 0,
                    "L2CachePTWMiss": 0,
                    "L2CachePTWMSHRHit": 0,
                    "L1VTLBHit": 0,
                    "L1VTLBMiss": 0,
                    "L3TLBHit": 0,
                    "L3TLBMiss": 0,
                    "InstCount": 0,
                },
                ignore_index=True,
            )

            continue

        df = pd.concat([df, parsed_df], ignore_index=True)

    df.to_csv(os.path.join(args.outDir, "mpw_miss_rate_report.csv"), index=False)
