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


def get_booksim_tlb_nodes(
    config: str,
) -> list:
    if config == "monolithic":
        nodes = []

        for i in range(96, 192):
            nodes.append(i)

        nodes.append(209)

        return nodes
    elif config == "SMSide":
        nodes = []

        for i in range(96, 192):
            nodes.append(i)

        for i in range(224, 240):
            nodes.append(i)

        return nodes
    else:
        assert False, "Unsupported configuration"


def get_booksim_mem_nodes(
    config: str,
) -> list:
    if config == "monolithic":
        nodes = []

        for i in range(0, 96):
            nodes.append(i)

        for i in range(193, 209):
            nodes.append(i)

        return nodes
    elif config == "SMSide":
        nodes = []

        for i in range(0, 96):
            nodes.append(i)

        for i in range(208, 224):
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
