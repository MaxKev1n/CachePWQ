//
// Created by 陈子航 on 2025/10/29.
//

#ifndef INTERSIM2_BOOKSIM_SHIM_HPP
#define INTERSIM2_BOOKSIM_SHIM_HPP

#ifdef __cplusplus
extern "C" {
#endif

    typedef void* booksim_net_t;

    static int recv_counter = 0;
    static int send_counter = 0;

    booksim_net_t booksim_create(const char* cfg_path, int n_shaders, int n_mems);
    void booksim_destroy(booksim_net_t net);
    void booksim_cycle(booksim_net_t net);

    int booksim_can_inject(booksim_net_t net, int src, int size_bytes);
    int booksim_inject(booksim_net_t net, int src, int dest,
                       unsigned long long pkt_id, int type, int size_bytes);

    int booksim_busy(booksim_net_t net);
    int booksim_peek(booksim_net_t net, int node, unsigned long long* pkt_id_out);
    void booksim_pop(booksim_net_t net, int node);

#ifdef __cplusplus
}
#endif

#endif //INTERSIM2_BOOKSIM_SHIM_HPP