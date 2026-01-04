__kernel void l2_slice_bandwidth(__global const unsigned int* restrict src, 
                                 __global unsigned int* restrict dst, 
                                 const unsigned int slice_stride_elements) {
    // slice_stride_elements = (N * S) / sizeof(unsigned int)
    int gid = get_global_id(0);
    
    // 计算该线程对应的、落在同一个 Slice 上的地址
    // 每一个线程只负责一个特定的偏移，确保所有线程的请求都指向同一个 Bank
    int target_idx = gid * slice_stride_elements;

    unsigned int data = src[target_idx];
    
    // 增加一点计算负载确保不被编译器优化，但保持 Memory-bound
    data += (unsigned int)(1); 

    dst[target_idx] = data;
}