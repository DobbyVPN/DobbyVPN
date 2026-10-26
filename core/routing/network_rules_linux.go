//go:build linux && !android

package routing

import (
	"encoding/binary"
	"fmt"

	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netlink/nl"
	"golang.org/x/sys/unix"
)

type linuxRuleIdentity struct {
	priority int
	table    int
	mark     uint32
	protocol uint8
}

// The pinned netlink RuleList does not populate Rule.Type. Read the kernel's
// fib_rule_hdr.action for the reserved priority range before matching ownership.
// Missing or ambiguous identities fail recovery. Ownership still requires the
// complete selector set, and deletion specifies the expected action.
func linuxNetworkRules(family, priority int) ([]netlink.Rule, error) {
	rules, err := linuxRuleList(family)
	if err != nil {
		return nil, err
	}
	var actions map[linuxRuleIdentity]uint8
	for i := range rules {
		rule := &rules[i]
		if rule.Priority < priority || rule.Priority > priority+2 || rule.Type != 0 {
			continue
		}
		if actions == nil {
			actions, err = linuxNativeRuleActions(family)
			if err != nil {
				return nil, err
			}
		}
		key := linuxRuleIdentity{rule.Priority, rule.Table, rule.Mark, rule.Protocol}
		action := actions[key]
		if action == 0 {
			return nil, fmt.Errorf("native routing rule action is missing or ambiguous: family=%d identity=%+v", family, key)
		}
		rule.Type = action
	}
	return rules, nil
}

func linuxNativeRuleActions(family int) (map[linuxRuleIdentity]uint8, error) {
	request := nl.NewNetlinkRequest(unix.RTM_GETRULE, unix.NLM_F_DUMP|unix.NLM_F_REQUEST)
	request.AddData(nl.NewIfInfomsg(family))
	messages, err := request.Execute(unix.NETLINK_ROUTE, unix.RTM_NEWRULE)
	if err != nil {
		return nil, fmt.Errorf("read native routing rule actions: %w", err)
	}
	actions := make(map[linuxRuleIdentity]uint8)
	for _, message := range messages {
		if len(message) < unix.SizeofRtMsg {
			return nil, fmt.Errorf("short native routing rule header: %d bytes", len(message))
		}
		header := nl.DeserializeRtMsg(message)
		if int(header.Family) != family {
			continue
		}
		attributes, err := nl.ParseRouteAttr(message[header.Len():])
		if err != nil {
			return nil, fmt.Errorf("decode native routing rule action: %w", err)
		}
		key := linuxRuleIdentity{table: int(header.Table)}
		for _, attribute := range attributes {
			switch attribute.Attr.Type {
			case nl.FRA_PRIORITY, nl.FRA_TABLE, nl.FRA_FWMARK:
				if len(attribute.Value) < 4 {
					return nil, fmt.Errorf("short native routing rule attribute %d", attribute.Attr.Type)
				}
				value := binary.NativeEndian.Uint32(attribute.Value)
				switch attribute.Attr.Type {
				case nl.FRA_PRIORITY:
					key.priority = int(value)
				case nl.FRA_TABLE:
					key.table = int(value)
				case nl.FRA_FWMARK:
					key.mark = value
				}
			case nl.FRA_PROTOCOL:
				if len(attribute.Value) == 0 {
					return nil, fmt.Errorf("short native routing rule protocol")
				}
				key.protocol = attribute.Value[0]
			}
		}
		if previous, exists := actions[key]; exists && previous != header.Type {
			actions[key] = 0
		} else if !exists {
			actions[key] = header.Type
		}
	}
	return actions, nil
}
