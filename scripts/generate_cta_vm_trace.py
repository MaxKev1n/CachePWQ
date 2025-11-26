import os
import json
import argparse

from plot.benchmark import benchmarks


def parse_trace_line(line):
    """
    Example trace line:
    4906741@GPU1.chiplet_00.SA_01.CU_00, cta-page-map, 4, 81100
    We need CTA index (3rd field) and address (4th field)
    """
    parts = [p.strip() for p in line.split(",")]
    if len(parts) < 4:
        return None

    try:
        cta_index = int(parts[2])
        addr = int(parts[3], 16)  # hex to int
        page_num = addr >> 12  # 4KB page number
        return cta_index, page_num
    except:
        return None


def process_trace(benchmark, input_dir, output_dir):
    trace_path = os.path.join(input_dir, f"{benchmark}.tlb.trace")
    if not os.path.exists(trace_path):
        raise FileNotFoundError(f"Trace file not found: {trace_path}")

    cta_pages = {}  # {cta_index: set(page_numbers)}

    with open(trace_path, "r") as f:
        for line in f:
            parsed = parse_trace_line(line)
            if not parsed:
                continue

            cta_index, page_num = parsed

            if cta_index not in cta_pages:
                cta_pages[cta_index] = set()

            cta_pages[cta_index].add(page_num)

    # --- SORT BY CTA INDEX ---
    cta_pages_json = {
        str(cta): sorted(list(pages))
        for cta, pages in sorted(cta_pages.items(), key=lambda x: x[0])
    }

    os.makedirs(output_dir, exist_ok=True)
    out_path = os.path.join(output_dir, f"{benchmark}.cta.json")

    with open(out_path, "w") as out_f:
        json.dump(cta_pages_json, out_f, indent=4)

    print(f"JSON file generated at: {out_path}")


def main():
    parser = argparse.ArgumentParser(description="Parse CTA page mapping trace")
    parser.add_argument("--input", required=True, help="Input folder")
    parser.add_argument("--output", required=True, help="Output folder")
    args = parser.parse_args()

    for benchmark in benchmarks:
        try:
            process_trace(benchmark, args.input, args.output)
        except Exception as e:
            print(f"Error processing benchmark '{benchmark}': {e}")


if __name__ == "__main__":
    main()
