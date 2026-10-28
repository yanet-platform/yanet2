package aclpb

import (
	"fmt"

	"github.com/yanet-platform/yanet2/modules/acl/bindings/go/cacl"
)

// ToActions converts proto actions into backend ACL actions.
//
// Returns an error if any action carries an unrecognized kind so the caller
// can reject the request rather than silently mapping the action to ALLOW.
func ToActions(protoActions []*Action) ([]cacl.ACLAction, error) {
	out := make([]cacl.ACLAction, len(protoActions))
	for idx, action := range protoActions {
		kind, err := action.ToCACLKind()
		if err != nil {
			return nil, fmt.Errorf("action %d: %w", idx, err)
		}

		out[idx].Kind = kind
	}

	return out, nil
}

// ToCACLKind maps the proto kind enum to the backend action kind.
func (m *Action) ToCACLKind() (uint32, error) {
	switch m.GetKind() {
	case ActionKind_ACTION_KIND_PASS:
		return uint32(cacl.ActionAllow), nil
	case ActionKind_ACTION_KIND_DENY:
		return uint32(cacl.ActionDeny), nil
	case ActionKind_ACTION_KIND_COUNT:
		return uint32(cacl.ActionCount), nil
	case ActionKind_ACTION_KIND_CHECK_STATE:
		return uint32(cacl.ActionCheckState), nil
	case ActionKind_ACTION_KIND_CREATE_STATE:
		return uint32(cacl.ActionCreateState), nil
	case ActionKind_ACTION_KIND_LOG:
		return uint32(cacl.ActionLog), nil
	default:
		return 0, fmt.Errorf("unknown action kind %d", m.GetKind())
	}
}
