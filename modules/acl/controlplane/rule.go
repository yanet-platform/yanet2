package acl

//#cgo CFLAGS: -I../../../
//#cgo LDFLAGS: -L../../../build/modules/acl/api -lacl_cp
//
//#include "api/agent.h"
//#include "modules/acl/api/module.h"
//#include "modules/acl/api/rule.h"
import "C"

type Rule struct {
	inner C.acl_rule_t
}

func NewRule()
