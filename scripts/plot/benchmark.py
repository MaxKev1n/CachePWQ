benchmarks = [
    "kmeans",
    "mis",
    "stencil2d",
    "pagerank",
    "matrixtranspose",
    "spmv",
    "gups",
    "gesummv",
    "simpleconvolution",
    "jacobi2d",
    "fastwalshtransform",
    "jacobi1d",
    "shoc-reduction",
]

low_mpki_benchmarks = [
    "simpleconvolution",
    "jacobi2d",
    "fastwalshtransform",
    "jacobi1d",
    "shoc-reduction",
]

high_mpki_benchmarks = [
    "kmeans",
    "mis",
    "stencil2d",
    "pagerank",
    "matrixtranspose",
    "spmv",
    "gups",
    "gesummv",
]


memory_overhead = {
    "convolution2d": 8129,
    "fastwalshtransform": 1660,
    "gups": 1300,
    "jacobi1d": 10894,
    "jacobi2d": 2883,
    "kmeans": 3174,
    "matrixtranspose": 1245,
    "mis": 1271,
    "pagerank": 5104,
    "shoc-reduction": 9313,
    "simpleconvolution": 10808,
    "stencil2d": 1367,
    "syr2k": 1045,
    "syrk": 1083,
    "spmv": 5837,
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
        "gesummv": "GEV",
    }

    return dict_short_names[benchmark]
