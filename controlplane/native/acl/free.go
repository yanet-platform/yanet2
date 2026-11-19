//go:build linux && (amd64 || arm64)

package acl

import (
	"unsafe"

	"github.com/yanet-platform/yanet2/controlplane/native/classify"
	"github.com/yanet-platform/yanet2/controlplane/native/cpconfig"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
)

// Free frees an ACL configuration NewConfig built once no live
// generation holds it, as acl_module_config_free does; a configuration
// a generation still holds reports cpconfig.ErrStillReferenced and stays.
func Free(module *layout.CpModule) error {
	config := (*layout.AclModuleConfig)(unsafe.Pointer(module))
	return cpconfig.DestroyModule(module, unsafe.Sizeof(layout.AclModuleConfig{}), func() { freeBody(config) })
}

// freeBody releases what the build wrote into a configuration, in the
// order of acl_module_config_destroy: the targets, the filters, then the
// classifiers they join. Every free is safe on a zeroed table, so a
// partially built configuration walks the same path.
func freeBody(config *layout.AclModuleConfig) {
	context := &config.CpModule.Entity.MemoryContext
	if targets := config.Targets.Get(); targets != nil {
		shmalloc.ContextFree(context, unsafe.Pointer(targets), unsafe.Sizeof(layout.AclTarget{})*uintptr(config.TargetCount))
		config.Targets.Set(nil)
	}

	classify.FreeDecode(&config.FilterL2.RuleMap)
	config.FilterL2 = layout.AclFilterL2{}
	classify.FreeJoin(&config.FilterIp4.RootJoint)
	classify.FreeJoin(&config.FilterIp4.PlainProtoJoint)
	classify.FreeDecode(&config.FilterIp4.RuleMap)
	config.FilterIp4 = layout.AclFilterIp4{}
	classify.FreeRuleTable(&config.FilterIp4Tcp.RootJoint)
	classify.FreeRuleTable(&config.FilterIp4Tcp.VacJoint)
	config.FilterIp4Tcp = layout.AclFilterIp4Tcp{}
	classify.FreeRuleTable(&config.FilterIp4Udp.RootJoint)
	classify.FreeDecode(&config.FilterIp4Udp.VacRuleMap)
	config.FilterIp4Udp = layout.AclFilterIp4Udp{}
	classify.FreeRuleTable(&config.FilterIp4Icmp.RootJoint)
	config.FilterIp4Icmp = layout.AclFilterIp4Icmp{}
	classify.FreeJoin(&config.FilterIp6.RootJoint)
	classify.FreeJoin(&config.FilterIp6.PlainProtoJoint)
	classify.FreeDecode(&config.FilterIp6.RuleMap)
	config.FilterIp6 = layout.AclFilterIp6{}
	classify.FreeRuleTable(&config.FilterIp6Tcp.RootJoint)
	classify.FreeRuleTable(&config.FilterIp6Tcp.VacJoint)
	config.FilterIp6Tcp = layout.AclFilterIp6Tcp{}
	classify.FreeRuleTable(&config.FilterIp6Udp.RootJoint)
	classify.FreeDecode(&config.FilterIp6Udp.VacRuleMap)
	config.FilterIp6Udp = layout.AclFilterIp6Udp{}
	classify.FreeRuleTable(&config.FilterIp6Icmp.RootJoint)
	config.FilterIp6Icmp = layout.AclFilterIp6Icmp{}

	classify.FreeDevice(&config.ClassifierL2.DevAttr)
	core4 := &config.ClassifierCore4
	classify.FreeDevice(&core4.DevAttr)
	classify.FreeNet4(&core4.Net4SrcAttr)
	classify.FreeNet4(&core4.Net4DstAttr)
	classify.FreeLine(&core4.IpprotoAttr)
	classify.FreeJoin(&core4.NetsJoint)
	classify.FreeJoin(&core4.MidJoint)
	classify.FreeJoin(&core4.ProtoJoint)
	*core4 = layout.AclClassifierCore4{}
	classify.FreeFragment(&config.ClassifierFrag4.FragAttr)
	classify.FreeLine(&config.ClassifierPlain4.IpprotoAttr)
	config.ClassifierPlain4 = layout.AclClassifierPlain{}
	freePorts(&config.ClassifierPorts4)
	freeTCP(&config.ClassifierTcp4)
	classify.FreeLine(&config.ClassifierIcmp4.TypeAttr)
	config.ClassifierIcmp4 = layout.AclClassifierIcmp{}

	core6 := &config.ClassifierCore6
	classify.FreeDevice(&core6.DevAttr)
	classify.FreeNet6(&core6.Net6SrcAttr)
	classify.FreeNet6(&core6.Net6DstAttr)
	classify.FreeLine(&core6.IpprotoAttr)
	classify.FreeJoin(&core6.NetsJoint)
	classify.FreeJoin(&core6.MidJoint)
	classify.FreeJoin(&core6.ProtoJoint)
	*core6 = layout.AclClassifierCore6{}
	classify.FreeFragment(&config.ClassifierFrag6.FragAttr)
	classify.FreeLine(&config.ClassifierPlain6.IpprotoAttr)
	config.ClassifierPlain6 = layout.AclClassifierPlain{}
	freePorts(&config.ClassifierPorts6)
	freeTCP(&config.ClassifierTcp6)
	classify.FreeLine(&config.ClassifierIcmp6.TypeAttr)
	config.ClassifierIcmp6 = layout.AclClassifierIcmp{}
}

func freePorts(ports *layout.AclClassifierPorts) {
	classify.FreePort(&ports.SrcAttr)
	classify.FreePort(&ports.DstAttr)
	classify.FreeJoin(&ports.Joint)
	*ports = layout.AclClassifierPorts{}
}

func freeTCP(tcp *layout.AclClassifierTcp) {
	classify.FreeLine(&tcp.FlagsAttr)
	classify.FreeJoin(&tcp.FlagsJoint)
	*tcp = layout.AclClassifierTcp{}
}
