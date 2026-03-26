# We are injected into AdGuard's tree, so we have direct access to their core targets!
add_library(dobby_bridge SHARED dobby_bridge.cpp)

# Link directly to the core engine, completely bypassing their broken vpn_easy wrapper
target_link_libraries(dobby_bridge PRIVATE
    vpnlibs_trusttunnel
    vpnlibs_core
    vpnlibs_net
    vpnlibs_common
)

# macOS/iOS require Apple's native networking and security frameworks
if(APPLE)
    target_link_libraries(dobby_bridge PRIVATE
        "-framework CoreFoundation"
        "-framework Security"
        "-framework Network"
    )
endif()

set_target_properties(dobby_bridge PROPERTIES OUTPUT_NAME "dobby_bridge")