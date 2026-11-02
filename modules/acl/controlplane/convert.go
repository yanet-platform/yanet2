package acl

import (
	"fmt"

	"github.com/yanet-platform/yanet2/modules/acl/bindings/go/cacl"
	aclpb "github.com/yanet-platform/yanet2/modules/acl/controlplane/aclpb/v1"
)

// toActions converts proto actions into backend ACL actions.
//
// Returns an error if any action carries an unrecognized kind so the caller
// can reject the request rather than silently mapping the action to ALLOW.
func toActions(protoActions []*aclpb.Action) ([]cacl.ACLAction, error) {
	out := make([]cacl.ACLAction, len(protoActions))
	for idx, action := range protoActions {
		kind, err := toCACLKind(action)
		if err != nil {
			return nil, fmt.Errorf("action %d: %w", idx, err)
		}

		out[idx].Kind = kind
	}

	return out, nil
}

// toCACLKind maps the proto kind enum to the backend action kind.
func toCACLKind(action *aclpb.Action) (uint32, error) {
	switch action.GetKind() {
	case aclpb.ActionKind_ACTION_KIND_PASS:
		return uint32(cacl.ActionAllow), nil
	case aclpb.ActionKind_ACTION_KIND_DENY:
		return uint32(cacl.ActionDeny), nil
	case aclpb.ActionKind_ACTION_KIND_COUNT:
		return uint32(cacl.ActionCount), nil
	case aclpb.ActionKind_ACTION_KIND_CHECK_STATE:
		return uint32(cacl.ActionCheckState), nil
	case aclpb.ActionKind_ACTION_KIND_CREATE_STATE:
		return uint32(cacl.ActionCreateState), nil
	case aclpb.ActionKind_ACTION_KIND_LOG:
		return uint32(cacl.ActionLog), nil
	default:
		return 0, fmt.Errorf("unknown action kind %d", action.GetKind())
	}
}
