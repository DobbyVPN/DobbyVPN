#include "dobby_bridge.h"

#include <memory>
#include <string>
#include <thread>
#include <optional>
#include <variant>
#include <future>

#include <magic_enum/magic_enum.hpp>
#include <toml++/toml.h>

// TrustTunnel Internal Headers (pulled directly from the submodule)
#include "common/logger.h"
#include "common/net_utils.h"
#include "net/tls.h"
#include "vpn/event_loop.h"
#include "vpn/platform.h"
#include "vpn/trusttunnel/auto_network_monitor.h"
#include "vpn/trusttunnel/client.h"
#include "vpn/trusttunnel/config.h"

static ag::Logger g_logger{"DobbyBridge"};

static dobby_on_log_message_t g_log_callback = nullptr;
static dobby_on_protect_socket_t g_protect_callback = nullptr;

void dobby_vpn_set_log_callback(dobby_on_log_message_t cb) {
    g_log_callback = cb;
    if (cb != nullptr) {
        ag::Logger::set_callback([](ag::LogLevel log_level, std::string_view message) {
            if (g_log_callback) {
                std::string msg_str(message);
                g_log_callback(static_cast<int>(log_level), msg_str.c_str());
            }
        });
    } else {
        ag::Logger::set_callback(nullptr);
    }
}

void dobby_vpn_set_protect_callback(dobby_on_protect_socket_t cb) {
    g_protect_callback = cb;
}

#ifdef _WIN32
static void vpn_windows_verify_certificate(ag::VpnVerifyCertificateEvent *event) {
    event->result = !!ag::tls_verify_cert(event->cert, event->chain, nullptr);
}
static INIT_ONCE g_init_once = INIT_ONCE_STATIC_INIT;
static HMODULE g_wintun_handle;
#endif

// --- Engine Management ---
struct dobby_vpn_context {
    std::unique_ptr<ag::TrustTunnelClient> client;
    std::unique_ptr<ag::AutoNetworkMonitor> network_monitor;
};

static dobby_vpn_context* m_vpn = nullptr;
static std::unique_ptr<ag::VpnEventLoop, decltype(&ag::vpn_event_loop_destroy)> m_ev_loop{nullptr, ag::vpn_event_loop_destroy};
static std::thread m_executor_thread;

void dobby_vpn_start(const char *toml_config, dobby_on_state_changed_t state_changed_cb, void *state_changed_cb_arg) {
    if (m_vpn) {
        warnlog(g_logger, "VPN is already running.");
        return;
    }

    // 1. Start Event Loop immediately
    m_ev_loop.reset(ag::vpn_event_loop_create());
    m_executor_thread = std::thread([]() { ag::vpn_event_loop_run(m_ev_loop.get()); });
    // ag::vpn_event_loop_dispatch_sync(m_ev_loop.get(), nullptr, nullptr);

    // 2. Capture the RAW string, which is safely copyable across threads
    std::string config_str(toml_config);

    ag::event_loop::submit(m_ev_loop.get(), [config_str, state_changed_cb, state_changed_cb_arg]() {
        
        // 3. Parse the TOML *inside* the background thread
        toml::parse_result parsed_config = toml::parse(config_str);
        if (!parsed_config) {
            errlog(g_logger, "Failed to parse TOML config");
            return;
        }

        auto trusttunnel_config = ag::TrustTunnelConfig::build_config(parsed_config);
        ag::vpn_post_quantum_group_set_enabled(trusttunnel_config->post_quantum_group_enabled);

        ag::VpnCallbacks callbacks;
#ifdef _WIN32
        if (std::holds_alternative<ag::TrustTunnelConfig::TunListener>(trusttunnel_config->listener)) {
            callbacks.protect_handler = [](ag::SocketProtectEvent *event) {
                event->result = !ag::vpn_win_socket_protect(event->fd, event->peer);
            };
        } else {
            callbacks.protect_handler = [](ag::SocketProtectEvent *event) {
                // If Go provides a callback, use it. Otherwise, default to 0 (Allow).
                event->result = g_protect_callback ? g_protect_callback(event->fd) : 0;
            };
        }
        callbacks.verify_handler = [](ag::VpnVerifyCertificateEvent *event) {
            vpn_windows_verify_certificate(event);
        };
#else
        callbacks.protect_handler = [](ag::SocketProtectEvent *event) {
            // If Go provides a callback, use it. Otherwise, default to 0 (Allow).
            event->result = g_protect_callback ? g_protect_callback(event->fd) : 0;
        };
#endif

        callbacks.state_changed_handler = [state_changed_cb, state_changed_cb_arg](ag::VpnStateChangedEvent *event) {
            infolog(g_logger, "State changed: {}", magic_enum::enum_name(event->state));
            if (state_changed_cb) state_changed_cb(state_changed_cb_arg, event->state);
        };

        // 4. Initialize the client safely
        m_vpn = new dobby_vpn_context();
        m_vpn->client = std::make_unique<ag::TrustTunnelClient>(std::move(*trusttunnel_config), std::move(callbacks));

        // m_vpn->network_monitor = std::make_unique<ag::AutoNetworkMonitor>(m_vpn->client.get());
        // m_vpn->network_monitor->start();
        m_vpn->client->connect(ag::TrustTunnelClient::AutoSetup{});
        
    }).release();
}

void dobby_vpn_stop() {
    infolog(g_logger, "Starting TrustTunnel vpn core stop.");

    // Always stop the event loop if it exists, even if m_vpn is currently null
    if (m_ev_loop) {
        std::promise<void> stop_promise;
        std::future<void> stop_future = stop_promise.get_future();

        // Submit cleanup task
        ag::event_loop::submit(m_ev_loop.get(), [&stop_promise]() {
            if (m_vpn) {
                // Let the destructor handle all the safe async teardown!
                // Do NOT manually call disconnect() or network_monitor->stop()
                delete m_vpn;
                m_vpn = nullptr;
            }
            stop_promise.set_value(); 
        }).release();

        // Wait for cleanup task to finish safely
        stop_future.wait();

        // Stop the loop
        ag::vpn_event_loop_stop(m_ev_loop.get());
    }

    // Always join the thread
    if (m_executor_thread.joinable()) {
        m_executor_thread.join();
    }

    // Free the event loop memory so it is completely clean for the next start
    m_ev_loop.reset();

    infolog(g_logger, "End TrustTunnel vpn core stop.");
}