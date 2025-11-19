package balancer

////////////////////////////////////////////////////////////////////////////////

// func TestPacketPlusUpdateReals(t *testing.T) {
// 	mock, err := test_utils.NewYanetMock(1<<22, 1<<27, []string{"balancer"})
// 	require.Nil(t, err, "failed to create mock: %w", err)
// 	defer mock.Free()

// 	agent, err := mock.AttachAgent("balancer", 1<<25)
// 	require.Nil(t, err, "failed to create agent: %w", err)

// 	config := balancer_cp.ModuleInstanceConfig{
// 		Services: []balancer_cp.VirtualService{
// 			{
// 				Address: IpAddr("192.166.13.22"),
// 				Port:    1000,
// 				Flags: balancer_cp.VsFlags{
// 					GRE:    false,
// 					OPS:    false,
// 					PureL3: false,
// 					FixMSS: false,
// 				},
// 				Proto: balancer_cp.TransportProtoTcp,
// 				AllowedSrc: []netip.Prefix{
// 					IpPrefix("10.12.0.0/8"),
// 				},
// 				Reals: []balancer_cp.Real{
// 					{
// 						Weight:  1,
// 						DstAddr: IpAddr("1.1.1.1"),
// 						SrcAddr: IpAddr("3.3.3.3"),
// 						SrcMask: IpAddr("255.240.255.0"),
// 						Enabled: true,
// 					},
// 				},
// 			},
// 		},
// 	}

// 	err = mock.PrepareForCpUpdate()
// 	require.Nil(t, err, "failed to prepare mock for cp update before create balancer instance")

// 	timeouts := balancer_cp.SessionsTimeouts{
// 		TcpSyn:  30,
// 		TcpFin:  60,
// 		Tcp:     30,
// 		Udp:     60,
// 		Default: 30,
// 	}

// 	balancer, err := balancer_cp.NewModuleInstance(agent, "balancer", &config, 100, &timeouts)
// 	require.Nil(t, err, "failed to create new balancer instance")
// 	defer balancer.Free()

// 	inLayers := MakeTCPPacket("10.12.15.1", 1005, "192.166.13.22", 1000, &layers.TCP{SYN: true})
// 	originPacket := common.LayersToPacket(t, inLayers...)
// 	t.Log("Origin packet", originPacket)

// 	result, err := HandlePackets(balancer, mock, originPacket)
// 	require.Nil(t, err, "failed to handle packet1: %s", err)

// 	require.True(t, len(result.Output) == 1, "failed to handle packet #1")
// 	require.True(t, len(result.Input) == 0)
// 	require.True(t, len(result.Drop) == 0)

// 	resultPacket := result.Output[0]
// 	t.Log("Result packet", resultPacket)
// 	require.True(t, resultPacket.IsTunneled, "result packet is not tunneled")

// 	err = mock.PrepareForCpUpdate()
// 	require.Nil(t, err, "failed to prepare mock for cp update before update reals")

// 	err = balancer.UpdateReals([]*balancerpb.RealUpdate{
// 		{
// 			VirtualIp: []byte("192.166.13.22"),
// 			Proto:     balancer_cp.TransportProtoTcp.IntoProto(),
// 			Port:      1000,
// 			RealIp:    []byte("1.1.1.1"),
// 			Weight:    5,
// 			Enable:    true,
// 		},
// 	}, true)
// 	require.NoError(t, err, "failed to handle real update")

// 	flushed, err := balancer.FlushRealUpdatesBuffer()
// 	require.NoError(t, err, "failed to flush real updates buffer")
// 	require.Equal(t, uint32(1), flushed)

// 	require.Equal(t, uint16(5), balancer.GetConfig().Services[0].Reals[0].Weight)

// 	info, err := balancer.StateInfo()
// 	require.NoError(t, err, "failed to get state info")

// 	t.Log("state info", info.JsonPretty())

// 	require.NotEqual(t, balancer.GetConfig().Services[0].Idx, int64(-1), "failed to init service indices")
// }

// ////////////////////////////////////////////////////////////////////////////////

// func TestGRE(t *testing.T) {
// 	mock, err := test_utils.NewYanetMock(1<<20, 1<<27, []string{"balancer"})
// 	require.Nil(t, err, "failed to create mock: %w", err)
// 	defer mock.Free()

// 	agent, err := mock.AttachAgent("balancer", 1<<24)
// 	require.Nil(t, err, "failed to attach agent: %w", err)

// 	config := balancer_cp.ModuleInstanceConfig{
// 		Services: []balancer_cp.VirtualService{
// 			{
// 				Address: IpAddr("192.166.13.22"),
// 				Port:    1000,
// 				Flags: balancer_cp.VsFlags{
// 					GRE:    true,
// 					OPS:    false,
// 					PureL3: false,
// 					FixMSS: false,
// 				},
// 				Proto: balancer_cp.TransportProtoTcp,
// 				AllowedSrc: []netip.Prefix{
// 					IpPrefix("10.12.0.0/8"),
// 				},
// 				Reals: []balancer_cp.Real{
// 					{
// 						Weight:  1,
// 						DstAddr: IpAddr("1.1.1.1"),
// 						SrcAddr: IpAddr("3.3.3.3"),
// 						SrcMask: IpAddr("255.240.255.0"),
// 						Enabled: true,
// 					},
// 				},
// 			},
// 		},
// 	}

// 	err = mock.PrepareForCpUpdate()
// 	require.Nil(t, err, "failed to prepare for cp update")

// 	timeouts := balancer_cp.SessionsTimeouts{
// 		TcpSynAck: 60,
// 		TcpSyn:    30,
// 		TcpFin:    60,
// 		Tcp:       30,
// 		Udp:       60,
// 		Default:   30,
// 	}

// 	balancer, err := balancer_cp.NewModuleInstance(agent, "balancer", &config, 100, &timeouts)
// 	require.Nil(t, err, "failed to create new balancer instance")
// 	defer balancer.Free()

// 	inLayers := MakeTCPPacket("10.12.15.1", 1005, "192.166.13.22", 1000, &layers.TCP{SYN: true})
// 	originPacket := common.LayersToPacket(t, inLayers...)
// 	t.Log("Origin packet", originPacket)

// 	result, err := HandlePackets(balancer, mock, originPacket)
// 	require.Nil(t, err, "failed to handle packet1: %s", err)

// 	require.True(t, len(result.Output) == 1, "failed to handle packet #1")
// 	require.True(t, len(result.Input) == 0)
// 	require.True(t, len(result.Drop) == 0)

// 	resultPacket := result.Output[0]
// 	require.True(t, resultPacket.IsTunneled, "result packet is not tunneled")
// 	require.Equal(t, resultPacket.TunnelType, "gre-ip4", "tunnel type must be GRE")

// 	require.Equal(t, resultPacket.Protocol, layers.IPProtocolGRE)
// 	require.Equal(t, resultPacket.DstIP.String(), "1.1.1.1")
// 	require.Equal(t, resultPacket.DstPort, uint16(1000))

// 	require.Equal(t, resultPacket.InnerPacket.DstIP.String(), "192.166.13.22")
// 	require.Equal(t, resultPacket.InnerPacket.SrcIP.String(), "10.12.15.1")
// 	require.Equal(t, resultPacket.InnerPacket.Protocol, layers.IPProtocolTCP)
// }

////////////////////////////////////////////////////////////////////////////////

// func TestWlc(t *testing.T) {
// 	mock, err := test_utils.NewYanetMock(1<<20, 1<<28, []string{"balancer"})
// 	require.Nil(t, err, "failed to create mock: %w", err)
// 	defer mock.Free()

// 	agent, err := mock.AttachAgent("balancer", 1<<27)
// 	require.Nil(t, err, "failed to attach agent: %w", err)

// 	packetCount := 10
// 	// (uint64)packetCount

// 	config := balancer_cp.ModuleInstanceConfig{
// 		Services: []balancer_cp.VirtualService{
// 			{
// 				Address: IpAddr("192.166.13.22"),
// 				Port:    1000,
// 				Flags: balancer_cp.VsFlags{
// 					GRE:    false,
// 					OPS:    false,
// 					PureL3: false,
// 					FixMSS: false,
// 				},
// 				Scheduler: balancer_cp.VsSchedulerPRR,
// 				Proto:     balancer_cp.TransportProtoTcp,
// 				AllowedSrc: []netip.Prefix{
// 					IpPrefix("10.12.0.0/8"),
// 				},
// 				Reals: []balancer_cp.Real{
// 					{
// 						Weight:  1,
// 						DstAddr: IpAddr("1.1.1.1"),
// 						SrcAddr: IpAddr("3.3.3.3"),
// 						SrcMask: IpAddr("255.240.255.0"),
// 						Enabled: true,
// 					},
// 					{
// 						Weight:  2,
// 						DstAddr: IpAddr("2.2.2.2"),
// 						SrcAddr: IpAddr("3.3.3.3"),
// 						SrcMask: IpAddr("255.240.255.0"),
// 						Enabled: true,
// 					},
// 					{
// 						Weight:  2,
// 						DstAddr: IpAddr("3.3.3.3"),
// 						SrcAddr: IpAddr("3.3.3.3"),
// 						SrcMask: IpAddr("255.240.255.0"),
// 						Enabled: true,
// 					},
// 				},
// 			},
// 		},
// 	}

// 	err = mock.PrepareForCpUpdate()
// 	require.Nil(t, err, "failed to prepare for cp update")

// 	timeouts := balancer_cp.SessionsTimeouts{
// 		TcpSynAck: 60,
// 		TcpSyn:    60,
// 		TcpFin:    60,
// 		Tcp:       60,
// 		Udp:       60,
// 		Default:   60,
// 	}

// 	balancer, err := balancer_cp.NewModuleInstance(agent, "balancer", &config, 2000, &timeouts)
// 	require.Nil(t, err, "failed to create new balancer instance")
// 	defer balancer.Free()

// 	firstRealPackets := make([]gopacket.Packet, 0)
// 	for i := range packetCount {
// 		addr := fmt.Sprintf("10.12.15.%d", i)
// 		inLayers := MakeTCPPacket(addr, 1005, "192.166.13.22", 1000, &layers.TCP{SYN: true})
// 		packet := common.LayersToPacket(t, inLayers...)
// 		firstRealPackets = append(firstRealPackets, packet)
// 	}

// 	result, err := HandlePackets(balancer, mock, firstRealPackets...)
// 	require.Nil(t, err, "failed to handle packets to the first real: %s", err)

// 	require.Equal(t, packetCount, len(result.Output), "failed to handle some packets to the first real")
// 	require.True(t, len(result.Input) == 0)
// 	require.True(t, len(result.Drop) == 0)

// 	t.Log("first real handled packets")

// 	info, err := balancer.StateInfo()
// 	require.Nil(t, err, "failed to get balancer info")
// 	t.Log(info.JsonPretty())

// 	updates := []*balancerpb.RealUpdate{
// 		{
// 			VirtualIp: []byte("192.166.13.22"),
// 			Proto:     balancer_cp.TransportProtoTcp.IntoProto(),
// 			Port:      1000,
// 			RealIp:    []byte("2.2.2.2"),
// 			Weight:    2,
// 			Enable:    true,
// 		},
// 		{
// 			VirtualIp: []byte("192.166.13.22"),
// 			Proto:     balancer_cp.TransportProtoTcp.IntoProto(),
// 			Port:      1000,
// 			RealIp:    []byte("3.3.3.3"),
// 			Weight:    2,
// 			Enable:    true,
// 		},
// 	}

// 	err = mock.PrepareForCpUpdate()
// 	require.Nil(t, err, "mock: failed to prepare for cp update")

// 	err = balancer.UpdateReals(updates, false)
// 	require.Nil(t, err, "failed to update reals")

// 	// packets := make([]gopacket.Packet, 0, packetCount)
// 	// for i := range packetCount {
// 	// 	addr := fmt.Sprintf("10.12.15.%d", i+100)
// 	// 	inLayers := MakeTCPPacket(addr, 1005, "192.166.13.22", 1000, &layers.TCP{SYN: true})
// 	// 	packet := common.LayersToPacket(t, inLayers...)
// 	// 	packets = append(packets, packet)
// 	// }

// 	// result, err = HandlePackets(balancer, mock, packets...)
// 	// require.Nil(t, err, "failed to handle packets after enable reals: %s", err)

// 	// require.Equal(t, packetCount, len(result.Output), "failed to handle some packets after enable reals")
// 	// require.Equal(t, 0, len(result.Input))
// 	// require.Equal(t, 0, len(result.Drop))

// 	// t.Log("send other packets")

// 	info, err = balancer.StateInfo()
// 	require.Nil(t, err, "failed to get balancer info")
// 	t.Log(info.JsonPretty())
// }
