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
    "simpleconvolution",
    "shoc-reduction",
    "spmv",
    "stencil2d",
    "syrk",
    "syr2k",
]

low_mpki_benchmarks = [
    "convolution2d",
    "fastwalshtransform",
    "jacobi1d",
    "jacobi2d",
    "mis",
    "simpleconvolution",
    "shoc-reduction",
]

high_mpki_benchmarks = [
    "gups",
    "kmeans",
    "matrixtranspose",
    "pagerank",
    "spmv",
    "stencil2d",
    "syrk",
    "syr2k",
]


memory_overhead = {
    "convolution2d": 33603,
    "fastwalshtransform": 12817,
    "gups": 1925,
    "jacobi1d": 43281,
    "jacobi2d": 47072,
    "kmeans": 28810,
    "matrixtranspose": 21837,
    "mis": 8281,
    "pagerank": 31996,
    "spmv": 43753,
    "shoc-reduction": 36918,
    "simpleconvolution": 33928,
    "stencil2d": 45778,
    "syr2k": 52848,
    "syrk": 26594,
}

def get_booksim_tlb_nodes(
    config: str,
) -> list:
    if config == "MemorySide":
        nodes = []

        for i in range(0, 193):
            nodes.append(i)

        return nodes
    elif config == "SMSide":
        nodes = []

        for i in range(0, 208):
            nodes.append(i)

        return nodes
    else:
        assert False, "Unsupported configuration"


def get_booksim_mem_nodes(
    config: str,
) -> list:
    if config == "MemorySide":
        nodes = []

        for i in range(0, 225):
            nodes.append(i)

        return nodes
    elif config == "SMSide":
        nodes = []

        for i in range(0, 240):
            nodes.append(i)

        return nodes
    else:
        assert False, "Unsupported configuration"


def get_benchmarks() -> list:
    """
    Returns a list of benchmarks.
    """
    return benchmarks


def get_short_name(benchmark: str) -> str:
    """
    Returns a short name for each benchmark.
    """
    dict_short_names = {
        "convolution2d": "C2D",
        "fastwalshtransform": "FWT",
        "gups": "GUPS",
        "jacobi1d": "J1D",
        "jacobi2d": "J2D",
        "kmeans": "KM",
        "matrixtranspose": "MT",
        "mis": "MIS",
        "pagerank": "PR",
        "simpleconvolution": "SC",
        "shoc-reduction": "RED",
        "spmv": "SPMV",
        "stencil2d": "ST",
        "syrk": "SYRK",
        "syr2k": "SYR2",
    }

    return dict_short_names[benchmark]
