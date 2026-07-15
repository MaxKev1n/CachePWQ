#!/bin/bash

module load cmake

module load compilers/gcc-13.1.0 

# 1. ensure the script is sourced, not executed
if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
    echo "❌ Error: This script must be SOURCED, not executed directly."
    echo "Usage: source $0"

    return 1
fi

# 2. set up the project environment variables
PROJECT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

export CAPWQ_ROOT="$PROJECT_DIR"

# 3. compile the shared library for accelwattch and intersim2
# echo "🔧 Compiling shared library for accelwattch..."

# cd $CAPWQ_ROOT/simulator/GPUWattch

# mkdir -p build

# cd build

# rm -rf *

# cmake ..

# make -j$(nproc)

# if [[ ! -f "libgpuwattch.so" ]]; then
#     echo "❌ Error: Failed to compile the shared library for accelwattch."

#     return 1
# fi

# echo "🔧 Compiling shared library for intersim2..."

# cd $CAPWQ_ROOT/simulator/intersim2

# mkdir -p build

# cd build

# rm -rf *

# cmake ..

# make -j$(nproc)

# if [[ ! -f "libintersim.so" ]]; then
#     echo "❌ Error: Failed to compile the shared library for intersim2."

#     return 1
# fi

# # 4. copy the compiled shared libraries to the simulator root for easy access

# cd $CAPWQ_ROOT/simulator

# mkdir -p libs

# cp $CAPWQ_ROOT/simulator/GPUWattch/build/libgpuwattch.so libs/

# cp $CAPWQ_ROOT/simulator/intersim2/build/libintersim.so libs/

# 5. set the LD_LIBRARY_PATH to include the libs directory

export CGO_CFLAGS="-I${PROJECT_DIR}/simulator/GPUWattch/ -I${PROJECT_DIR}/simulator/intersim2/"

export CGO_LDFLAGS="-L${PROJECT_DIR}/simulator/libs/ -lgpuwattch -lintersim"

export LD_LIBRARY_PATH="${PROJECT_DIR}/simulator/libs/:$LD_LIBRARY_PATH"

export PATH=/hpc2hdd/home/zchen097/go1.23/go/bin:$PATH

# 6. print success message and the value of the environment variable
echo "✅ Project environment variables have been set."

echo "PROJECT_ROOT is now: $CAPWQ_ROOT"