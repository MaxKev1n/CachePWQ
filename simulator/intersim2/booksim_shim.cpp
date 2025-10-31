#include "interconnect_interface.hpp"
#include "booksim_shim.hpp"
#include "intersim_config.hpp"
#include "globals.hpp"

struct booksim_net_wrap {
    InterconnectInterface* iface;
};

booksim_net_t booksim_create(const char* cfg_path, int n_nodes) {
    auto wrap = new booksim_net_wrap;
    wrap->iface = InterconnectInterface::New(cfg_path);

    wrap->iface->CreateInterconnect(385, 33);
    wrap->iface->Init();

    g_icnt_interface = wrap->iface; // for compatibility

    return wrap;
}

void booksim_destroy(booksim_net_t net) {
    auto wrap = reinterpret_cast<booksim_net_wrap*>(net);
    delete wrap->iface;
    delete wrap;
}

void booksim_cycle(booksim_net_t net) {
    auto wrap = reinterpret_cast<booksim_net_wrap*>(net);
    wrap->iface->Advance();
}

// injection API
int booksim_can_inject(booksim_net_t net, int src, int n_flits) {
    auto wrap = reinterpret_cast<booksim_net_wrap*>(net);
    return wrap->iface->HasBuffer(src, n_flits*wrap->iface->GetFlitSize());
}

int booksim_inject(booksim_net_t net, int src, int dst,
                   unsigned long long pkt_id, int type, int size_bytes) {
    auto wrap = reinterpret_cast<booksim_net_wrap*>(net);
    wrap->iface->Push(src, dst, (void*)pkt_id, type, size_bytes);
    recv_counter++;
    return 1;
}

int booksim_busy(booksim_net_t net) {
    auto wrap = reinterpret_cast<booksim_net_wrap*>(net);
    return wrap->iface->Busy() ? 1 : 0;
}

int booksim_peek(booksim_net_t net, int node, unsigned long long* pkt_id_out) {
    auto wrap = reinterpret_cast<booksim_net_wrap*>(net);
    auto data = wrap->iface->Get(node);
    if (data) {
        *pkt_id_out = reinterpret_cast<unsigned long long>(data);
        return 1;
    }
    return 0;
}

void booksim_pop(booksim_net_t net, int node) {
    auto wrap = reinterpret_cast<booksim_net_wrap*>(net);
    auto data = wrap->iface->Pop(node);

    assert(data == nullptr);
    send_counter++;
}