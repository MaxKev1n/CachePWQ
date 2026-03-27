benchmarks = [
    "fastwalshtransform",
    "gups",
    "jacobi1d",
    "jacobi2d",
    "kmeans",
    "matrixtranspose",
    "mis",
    "pagerank",
    "spmv",
    "simpleconvolution",
    "shoc-reduction",
    "stencil2d",
    "gesummv",
]


memory_overhead = {
    "fastwalshtransform": 12817,
    "gups": 25571,
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
    "gesummv": 45778,
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
        "gesummv": "GEV",
    }

    return dict_short_names[benchmark]
