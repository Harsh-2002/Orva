#include <algorithm>
#include <cassert>
#include <cstdio>
#include <deque>
#include "nstun/tcp.cc"

static int target_fd = -1;
static std::deque<ssize_t> writes;
static unsigned send_calls = 0;
static uint32_t interest = 0;
static bool fail_epoll = false;

extern "C" ssize_t __real_send(int, const void*, size_t, int);
extern "C" int __real_epoll_ctl(int, int, int, epoll_event*);
extern "C" ssize_t __wrap_send(int fd, const void* data, size_t len, int flags) {
    if (fd != target_fd) return __real_send(fd, data, len, flags);
    ++send_calls;
    assert(flags & MSG_NOSIGNAL);
    if (!writes.empty()) {
        ssize_t result = writes.front();
        writes.pop_front();
        if (result < 0) { errno = -result; return -1; }
        if (result == 0) return 0;
        len = std::min(len, static_cast<size_t>(result));
    }
    return __real_send(fd, data, len, flags);
}
extern "C" int __wrap_epoll_ctl(int epfd, int op, int fd, epoll_event* ev) {
    if (fd == target_fd && op == EPOLL_CTL_MOD) {
        if (fail_epoll) { fail_epoll = false; errno = ENOENT; return -1; }
        interest = ev->events;
    }
    return __real_epoll_ctl(epfd, op, fd, ev);
}

struct Fixture {
    nstun::Context ctx = {};
    nstun::TcpFlow* flow;
    int peer;
    std::vector<uint8_t> received;
    Fixture(bool paused = false) {
        int fds[2];
        assert(socketpair(AF_UNIX, SOCK_STREAM | SOCK_NONBLOCK, 0, fds) == 0);
        target_fd = fds[0]; peer = fds[1];
        writes.clear(); send_calls = 0; fail_epoll = false;
        ctx.epoll_fd = epoll_create1(EPOLL_CLOEXEC);
        ctx.tap_fd = open("/dev/null", O_WRONLY | O_CLOEXEC);
        assert(ctx.epoll_fd >= 0 && ctx.tap_fd >= 0);
        auto owned = std::make_unique<nstun::TcpFlow>();
        flow = owned.get(); flow->host_fd = target_fd; flow->key4 = {};
        flow->state = nstun::TcpState::ESTABLISHED;
        flow->epoll_in_disabled = paused;
        ctx.flows_by_fd[target_fd] = flow;
        ctx.ipv4_tcp_flows_by_key[flow->key4] = std::move(owned);
        interest = EPOLLERR | EPOLLHUP | (paused ? 0 : uint32_t(EPOLLIN));
        epoll_event ev = {.events = interest, .data = {.fd = target_fd}};
        assert(epoll_ctl(ctx.epoll_fd, EPOLL_CTL_ADD, target_fd, &ev) == 0);
    }
    ~Fixture() { close(peer); close(ctx.epoll_fd); close(ctx.tap_fd); target_fd = -1; }
    void read_available() {
        uint8_t buf[65536];
        ssize_t count;
        while ((count = recv(peer, buf, sizeof(buf), MSG_DONTWAIT)) > 0)
            received.insert(received.end(), buf, buf + count);
        assert(count == 0 || errno == EAGAIN || errno == EWOULDBLOCK);
    }
    void drain() {
        for (unsigned steps = 0; !flow->rx_buffer.empty(); ++steps) {
            assert(steps < 1000);
            read_available();
            epoll_event ev;
            assert(epoll_wait(ctx.epoll_fd, &ev, 1, 1000) == 1);
            assert(ev.events & EPOLLOUT);
            nstun::handle_host_tcp(&ctx, flow, ev.events);
            assert(ctx.flows_by_fd.count(target_fd));
        }
        read_available();
        assert(flow->rx_sent_offset == 0 && !flow->epoll_out_registered);
        assert(!(interest & EPOLLOUT));
        epoll_event ev;
        assert(epoll_wait(ctx.epoll_fd, &ev, 1, 0) == 0);
    }
};

static std::vector<uint8_t> payload(size_t size) {
    std::vector<uint8_t> result(size);
    for (size_t i = 0; i < size; ++i) result[i] = (i * 131 + i / 251) % 256;
    return result;
}

static void short_write(bool paused) {
    Fixture f(paused);
    auto expected = payload(57351);
    f.flow->rx_buffer = expected;
    writes = {49232};
    assert(!nstun::flush_to_host(&f.ctx, f.flow));
    assert(send_calls == 1 && f.flow->rx_sent_offset == 49232);
    assert(f.flow->rx_buffer.size() - f.flow->rx_sent_offset == 8119);
    assert(f.flow->epoll_out_registered && (interest & EPOLLOUT));
    assert(bool(interest & EPOLLIN) == !paused);
    f.drain();
    assert(f.received == expected);
    assert(bool(interest & EPOLLIN) == !paused);
}

static void repeated_writes_and_append() {
    Fixture f;
    auto expected = payload(65536);
    f.flow->rx_buffer = expected;
    writes = {7, -EAGAIN, 11, -EWOULDBLOCK, -EINTR, 13, 31, 1024};
    assert(!nstun::flush_to_host(&f.ctx, f.flow));
    auto extra = payload(8192);
    f.flow->rx_buffer.insert(f.flow->rx_buffer.end(), extra.begin(), extra.end());
    expected.insert(expected.end(), extra.begin(), extra.end());
    f.drain();
    assert(writes.empty() && f.received == expected);
}

static void interrupted_and_blocked() {
    Fixture f;
    auto expected = payload(1024);
    f.flow->rx_buffer = expected;
    writes.assign(16, -EINTR);
    assert(!nstun::flush_to_host(&f.ctx, f.flow));
    assert(send_calls == 16 && f.flow->rx_sent_offset == 0);
    assert(f.flow->epoll_out_registered);
    writes = {-EAGAIN, 23};
    f.drain();
    assert(f.received == expected);
}

static void real_backpressure() {
    Fixture f;
    int size = 4096;
    assert(setsockopt(target_fd, SOL_SOCKET, SO_SNDBUF, &size, sizeof(size)) == 0);
    auto expected = payload(65536);
    f.flow->rx_buffer = expected;
    assert(!nstun::flush_to_host(&f.ctx, f.flow));
    size_t offset = f.flow->rx_sent_offset;
    assert(offset > 0 && offset < expected.size());
    assert(!nstun::flush_to_host(&f.ctx, f.flow));
    assert(f.flow->rx_sent_offset == offset && f.flow->epoll_out_registered);
    f.drain();
    assert(f.received == expected);
}

static void terminal_errors() {
    for (ssize_t result : {ssize_t(0), ssize_t(-EPIPE), ssize_t(-ECONNRESET)}) {
        Fixture f;
        f.flow->rx_buffer = payload(128);
        writes = {result};
        assert(nstun::flush_to_host(&f.ctx, f.flow));
        assert(f.ctx.flows_by_fd.empty() && f.ctx.ipv4_tcp_flows_by_key.empty());
    }
    {
        Fixture f;
        f.flow->rx_buffer = payload(128); writes = {-EAGAIN}; fail_epoll = true;
        assert(nstun::flush_to_host(&f.ctx, f.flow));
        assert(f.ctx.flows_by_fd.empty());
    }
    {
        Fixture f;
        f.flow->rx_buffer = payload(128); writes = {7};
        assert(!nstun::flush_to_host(&f.ctx, f.flow));
        fail_epoll = true;
        assert(nstun::flush_to_host(&f.ctx, f.flow));
        assert(f.ctx.flows_by_fd.empty());
    }
    {
        Fixture f;
        f.flow->rx_buffer = payload(128); writes = {-EAGAIN};
        assert(!nstun::flush_to_host(&f.ctx, f.flow));
        close(f.peer); f.peer = -1;
        assert(nstun::flush_to_host(&f.ctx, f.flow));
        assert(f.ctx.flows_by_fd.empty());
    }
}

static void buffer_bound_and_fin() {
    {
        Fixture f;
        f.flow->rx_buffer = payload(nstun::TCP_RX_BUFFER_HARD_CAP);
        nstun::tcp_hdr tcp = {};
        std::vector<uint8_t> packet(sizeof(tcp) + 1);
        nstun::tcp_process_data(&f.ctx, f.flow, &tcp, packet, sizeof(tcp));
        assert(f.flow->rx_buffer.size() == nstun::TCP_RX_BUFFER_HARD_CAP);
        assert(f.flow->ack_to_guest == 0 && send_calls == 0);
    }
    {
        Fixture f;
        auto expected = payload(4096);
        f.flow->rx_buffer = expected; writes = {17};
        assert(!nstun::flush_to_host(&f.ctx, f.flow));
        nstun::tcp_hdr tcp = {};
        tcp.flags = nstun::NSTUN_TCP_FLAG_FIN;
        std::vector<uint8_t> packet(sizeof(tcp));
        nstun::tcp_process_data(&f.ctx, f.flow, &tcp, packet, sizeof(tcp));
        assert(f.flow->guest_eof && f.flow->epoll_out_registered);
        f.read_available();
        uint8_t byte;
        assert(recv(f.peer, &byte, 1, MSG_DONTWAIT) == -1 && errno == EAGAIN);
        f.drain();
        assert(f.received == expected);
        assert(recv(f.peer, &byte, 1, MSG_DONTWAIT) == 0);
    }
}

int main() {
    short_write(false); short_write(true);
    repeated_writes_and_append(); interrupted_and_blocked();
    real_backpressure(); terminal_errors(); buffer_bound_and_fin();
    puts("NSTUN TCP regression: positive short writes, offsets, append, EAGAIN, EINTR, disconnect, epoll failure, buffer cap, FIN: PASS");
}
