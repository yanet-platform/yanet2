package rib

import (
	"sync"
	"time"
)

// Pool for the Route structs
var (
	routeStructPool sync.Pool = sync.Pool{
		New: func() any {
			return &Route{}
		},
	}
	largeCommunityListStructPool sync.Pool = sync.Pool{
		New: func() any {
			return &LargeCommunityList{}
		},
	}
)

func FreeRoute(r *Route) {
	next := r.LargeCommunities
	for next != nil {
		prev := next
		next = next.Next
		freeLargeCommunity(prev)
	}
	*r = Route{} // clear
	routeStructPool.Put(r)
}

func makeRoute() *Route {
	r := routeStructPool.Get().(*Route)
	r.UpdatedAt = time.Now()
	return r
}

func copyRoute(r *Route) *Route {
	cpy := makeRoute()
	*cpy = *r

	// Copy LargeCommunities
	cpy.LargeCommunities = nil
	copyListRef := &cpy.LargeCommunities
	for c := r.LargeCommunities; c != nil; c = c.Next {
		*copyListRef = largeCommunityListStructPool.Get().(*LargeCommunityList)
		(*copyListRef).LargeCommunity = c.LargeCommunity
		copyListRef = &(*copyListRef).Next
	}
	return cpy
}

func MakeStaticRoute() *Route {
	r := makeRoute()
	r.SourceID = RouteSourceStatic
	return r
}

func MakeBirdRoute() *Route {
	r := makeRoute()
	r.SourceID = RouteSourceBird
	return r
}

func freeLargeCommunity(c *LargeCommunityList) {
	*c = LargeCommunityList{} // clear
	largeCommunityListStructPool.Put(c)
}

func (m *Route) AddLargeCommunity(c *LargeCommunity) {
	cRef := largeCommunityListStructPool.Get().(*LargeCommunityList)
	cRef.LargeCommunity = *c
	target := &m.LargeCommunities
	for *target != nil {
		target = &(*target).Next
	}
	*target = cRef
}
