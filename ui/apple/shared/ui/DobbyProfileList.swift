import SwiftUI

/// Subscription inventory presentation shared by the Apple clients.
struct DobbyProfileList: View {
    @ObservedObject var model: DobbySessionViewModel
    @Binding var rowHeight: CGFloat
    let dismissSourceFocus: () -> Void

    var body: some View {
        let snapshot = model.snapshot
        let orphan = model.orphanProfile
        return ZStack {
            Color.tray.onTapGesture { dismissSourceFocus() }
            if snapshot.profiles.isEmpty && orphan == nil && !model.loadError.isEmpty {
                scrollableNotice {
                    VStack(spacing: pt(10)) {
                        SockIcon(holey: true).frame(width: pt(40), height: pt(50))
                        Text("Couldn't load the subscription")
                            .dobbyFont(15, .medium)
                            .foregroundColor(.ink)
                        Button { model.retryLoad() } label: {
                            Text("Retry")
                                .dobbyFont(15, .semibold)
                                .foregroundColor(.surface)
                                .padding(.horizontal, pt(22))
                                .frame(minHeight: pt(40))
                                .background(Color.ink, in: RoundedRectangle(cornerRadius: pt(8)))
                        }
                        .buttonStyle(.plain)
                        .accessibilityIdentifier("tray.retry")
                    }
                }
            } else if snapshot.profiles.isEmpty && orphan == nil {
                scrollableNotice { EmptyTray() }
            } else {
                ScrollView(showsIndicators: false) {
                    LazyVStack(spacing: pt(8)) {
                        if let orphan {
                            let orphanConnected = snapshot.state == "CONNECTED" && snapshot.pendingTarget == nil
                            ConnectionButton(
                                name: orphan.name,
                                details: [protocolName(orphan.protocolName), model.sessionStatus?.rawValue, "not in subscription"],
                                on: isOn(model.sessionStatus),
                                action: orphanConnected ? "Connected" : model.mainActionTitle
                            ) { dismissSourceFocus(); model.mainAction() }
                            .disabled(!model.mainActionEnabled || orphanConnected)
                            .accessibilityIdentifier("profile.active")
                        }
                        ForEach(snapshot.profiles) { profile in
                            let status = model.profileStatus(profile.index)
                            let title = model.profileActionTitle(profile.index)
                            ConnectionButton(
                                name: profile.name,
                                details: [protocolName(profile.protocolName), status?.rawValue],
                                on: isOn(status),
                                action: title
                            ) { dismissSourceFocus(); model.performPrimaryAction(profile.index) }
                            .disabled(!model.canAct(profile.index) || title == "Connected")
                            .accessibilityIdentifier("profile.\(snapshot.digest).\(profile.index).action")
                        }
                    }
                    .padding(pt(8))
                }
                .accessibilityIdentifier("Profile list viewport")
                .onPreferenceChange(RowHeight.self) { rowHeight = $0 }
                .background(GeometryReader { _ in
                    Color.clear.preference(key: TrayCardHeight.self, value: 0)
                })
            }
        }
        .clipShape(RoundedRectangle(cornerRadius: pt(14)))
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("connection.tray")
    }

    /// Notices keep their own ideal height: the card frame grows to it instead of capping the
    /// card below its fixed content, which used to paint the notice over the controls above.
    private func scrollableNotice<Content: View>(@ViewBuilder _ content: () -> Content) -> some View {
        let notice = content()
        return GeometryReader { tray in
            ScrollView {
                notice
                    .fixedSize(horizontal: false, vertical: true)
                    .background(GeometryReader { noticeGeometry in
                        Color.clear.preference(key: TrayCardHeight.self, value: noticeGeometry.size.height)
                    })
                    .frame(maxWidth: .infinity, minHeight: tray.size.height)
            }
        }
    }

    private func isOn(_ status: DobbyProfileStatus?) -> Bool {
        status == .connected || status == .reconnecting
    }

    private func protocolName(_ value: String) -> String? {
        switch value {
        case "": return nil
        case "OUTLINE": return "Outline"
        case "XRAY": return "Xray"
        case "TRUST_TUNNEL": return "TrustTunnel"
        default: return value
        }
    }

}

/// What the list area shows before any subscription is loaded.
private struct EmptyTray: View {
    var body: some View {
        VStack(spacing: pt(10)) {
            SockIcon(holey: false)
                .frame(width: pt(40), height: pt(50))
                .accessibilityElement()
                .accessibilityLabel("No subscription yet")
            Text("Paste the subscription link from your VPN provider above.")
                .dobbyFont(15)
                .foregroundColor(.muted)
                .multilineTextAlignment(.center)
                .padding(.horizontal, pt(16))
                .accessibilityIdentifier("tray.empty-hint")
        }
    }
}

private struct ConnectionButton: View {
    let name: String
    let details: [String?]
    let on: Bool
    let action: String
    let perform: () -> Void

    var body: some View {
        Button(action: perform) {
            HStack(spacing: pt(12)) {
                VStack(alignment: .leading, spacing: 0) {
                    Text(name)
                        .dobbyFont(17, .medium)
                        .foregroundColor(.ink)
                        .lineLimit(2)
                    Text(details.compactMap { $0 }.joined(separator: " · "))
                        .dobbyFont(13, on ? .semibold : .regular)
                        .foregroundColor(on ? .ink : .muted)
                        .lineLimit(1)
                }
                .frame(maxWidth: .infinity, alignment: .leading)
                Text(action)
                    .dobbyFont(15, .semibold)
                    .foregroundColor(.ink)
            }
            .padding(.horizontal, pt(14))
            .padding(.vertical, pt(8))
            .frame(minHeight: pt(56))
            .background(Color.surface, in: buttonShape)
            .overlay(buttonShape.stroke(Color.ink, lineWidth: on ? 3 : 1.5))
            .contentShape(buttonShape)
        }
        .buttonStyle(.plain)
        .accessibilityLabel(action)
        .accessibilityValue(([name] + details.compactMap { $0 }).joined(separator: " · "))
        .background(GeometryReader { Color.clear.preference(key: RowHeight.self, value: $0.size.height) })
    }
}

private struct RowHeight: PreferenceKey {
    static var defaultValue: CGFloat = 0
    static func reduce(value: inout CGFloat, nextValue: () -> CGFloat) { value = max(value, nextValue()) }
}

/// The fixed notice's ideal height, published so the tray frame never sits below its own content.
struct TrayCardHeight: PreferenceKey {
    static var defaultValue: CGFloat = 0
    static func reduce(value: inout CGFloat, nextValue: () -> CGFloat) { value = max(value, nextValue()) }
}

