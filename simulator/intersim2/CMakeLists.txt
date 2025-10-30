cmake_minimum_required(VERSION 3.10)
find_package(BISON REQUIRED)
find_package(FLEX REQUIRED)

set(CMAKE_CXX_FLAGS "${CMAKE_CXX_FLAGS} -g0 -O3 -gdwarf-4")
set(CMAKE_C_FLAGS   "${CMAKE_C_FLAGS}   -g0 -O3 -gdwarf-4")

execute_process(COMMAND xcrun --sdk macosx --show-sdk-path
        OUTPUT_VARIABLE MACOSX_SDK_PATH
        OUTPUT_STRIP_TRAILING_WHITESPACE)
include_directories(SYSTEM "${MACOSX_SDK_PATH}/usr/include/c++/v1")

option(GPGPUSIM_INTERSIM_STANDALONE "Whether to also build intersim in standalone mode" OFF)

# Specify Flex and Bison target
BISON_TARGET(intersim_config_parser config.y ${CMAKE_CURRENT_BINARY_DIR}/y.tab.c
        COMPILE_FLAGS "-y -d --file-prefix=${CMAKE_CURRENT_BINARY_DIR}/y")
FLEX_TARGET(intersim_config_lexer config.l ${CMAKE_CURRENT_BINARY_DIR}/lex.yy.c)
ADD_FLEX_BISON_DEPENDENCY(intersim_config_lexer intersim_config_parser)

# Set generated source files to CXX
set_source_files_properties(${BISON_intersim_config_parser_OUTPUT_SOURCE}
        ${FLEX_intersim_config_lexer_OUTPUTS}
        PROPERTIES LANGUAGE CXX)

# Create booksim or libintersim.a
# Shared include path
list(APPEND intersim_INC ${CMAKE_CURRENT_SOURCE_DIR}
        ${CMAKE_CURRENT_SOURCE_DIR}/allocators
        ${CMAKE_CURRENT_SOURCE_DIR}/arbiters
        ${CMAKE_CURRENT_SOURCE_DIR}/networks
        ${CMAKE_CURRENT_SOURCE_DIR}/power
        ${CMAKE_CURRENT_SOURCE_DIR}/routers
        ${PROJECT_SOURCE_DIR}/src)

# Shared source files
list(APPEND intersim_SRC
        ${BISON_intersim_config_parser_OUTPUT_SOURCE}
        ${FLEX_intersim_config_lexer_OUTPUTS}
        allocators/allocator.cpp
        allocators/islip.cpp
        allocators/loa.cpp
        allocators/maxsize.cpp
        allocators/pim.cpp
        allocators/selalloc.cpp
        allocators/separable.cpp
        allocators/separable_input_first.cpp
        allocators/separable_output_first.cpp
        allocators/wavefront.cpp
        arbiters/arbiter.cpp
        arbiters/matrix_arb.cpp
        arbiters/prio_arb.cpp
        arbiters/roundrobin_arb.cpp
        arbiters/tree_arb.cpp
        batchtrafficmanager.cpp
        booksim_config.cpp
        booksim_shim.cpp
        buffer.cpp
        buffer_state.cpp
        config_utils.cpp
        credit.cpp
        flitchannel.cpp
        flit.cpp
        gputrafficmanager.cpp
        injection.cpp
        interconnect_interface.cpp
        intersim_config.cpp
        main.cpp
        misc_utils.cpp
        module.cpp
        networks/anynet.cpp
        networks/cmesh.cpp
        networks/dragonfly.cpp
        networks/fattree.cpp
        networks/flatfly_onchip.cpp
        networks/fly.cpp
        networks/kncube.cpp
        networks/network.cpp
        networks/qtree.cpp
        networks/tree4.cpp
        outputset.cpp
        packet_reply_info.cpp
        power/buffer_monitor.cpp
        power/power_module.cpp
        power/switch_monitor.cpp
        rng_double_wrapper.cpp
        rng_wrapper.cpp
        routefunc.cpp
        routers/chaos_router.cpp
        routers/event_router.cpp
        routers/iq_router.cpp
        routers/router.cpp
        stats.cpp
        traffic.cpp
        trafficmanager.cpp
        vc.cpp)

# If standalone, also build for it
if(GPGPUSIM_INTERSIM_STANDALONE)
    list(REMOVE_ITEM ${intersim_SRC} interconnect_interface.cpp)
    add_executable(booksim ${intersim_SRC})
    target_include_directories(booksim PUBLIC
            ${intersim_INC})
    target_include_directories(booksim PRIVATE ${CMAKE_CURRENT_BINARY_DIR})
    target_include_directories(booksim PUBLIC ${CUDAToolkit_INCLUDE_DIRS})
    # Remove globally set TRACING_ON flag
    target_compile_options(booksim PRIVATE -UTRACING_ON)
endif()

# ===================================================================
# ↓↓↓↓↓↓↓↓↓↓↓↓↓↓↓↓↓↓↓↓↓↓↓↓↓↓↓↓↓↓↓↓↓↓↓↓↓↓↓↓↓↓↓↓↓↓↓↓↓↓↓↓↓↓↓↓↓↓↓↓↓↓↓↓↓
# 修改 1: 生成共享库而不是静态库
# 修改 2: 允许在 cgo / MGPUSim 动态链接 (PIC)
# 修改 3: 移除 main.cpp（否则无法作为库链接）
# 修改 4: 设置输出名为 libintersim.dylib
# ===================================================================

# 移除 main.cpp（否则 cgo 链接会重复定义 main）
list(REMOVE_ITEM intersim_SRC ${CMAKE_CURRENT_SOURCE_DIR}/main.cpp)

# 启用位置无关代码 (for shared library / cgo)
set(CMAKE_POSITION_INDEPENDENT_CODE ON)

# 构建为共享库
add_library(intersim SHARED ${intersim_SRC})
set_target_properties(intersim PROPERTIES OUTPUT_NAME "intersim")

target_include_directories(intersim PUBLIC
        ${intersim_INC}
        ${PROJECT_SOURCE_DIR}/src/gpgpu-sim)
target_include_directories(intersim PRIVATE ${CMAKE_CURRENT_BINARY_DIR})
target_include_directories(intersim PUBLIC ${CUDAToolkit_INCLUDE_DIRS})
target_compile_definitions(intersim PRIVATE CREATE_LIBRARY)
target_compile_options(intersim PRIVATE -UTRACING_ON)