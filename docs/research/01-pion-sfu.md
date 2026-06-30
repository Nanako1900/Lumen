# Pion WebRTC v4 — 音频 SFU 关键实现细节调研

> 调研日期: 2026-06-29
> 目标版本: `github.com/pion/webrtc/v4` 最新稳定版 **v4.2.5**(2026-02-12 发布)
> 配套库: `github.com/pion/ice/v4`(UDPMux / NAT1To1 / mDNS 控制)
> 适用场景: VPS(公网 IP) + 容器化部署、单 UDP 端口、仅音频 Opus、多人房间 SFU 转发

---

## 0. 版本与导入说明

- 最新稳定版 **v4.2.5**(2026-02-12),修了 DTLS 与 OpenSSL 互通问题。
- v4.2.3-securityfix 修了 **CVE-2026-26014**(DTLS),生产务必用 >= v4.2.5。
- v4.1.0 起 Pion 改为「每月最后一个周末」固定发版节奏;v4.2.0 是 2025 收官大版本(含 SCTP RACK、ICE renomination)。
- 导入路径必须显式带 `/v4`:

```go
import (
    "github.com/pion/webrtc/v4"
    "github.com/pion/ice/v4"
    "github.com/pion/rtp"
)
```

注意:webrtc v4 配套的是 ice **v4**(不是网上很多老示例里的 ice/v2)。`UDPMuxFromPort*` 选项集在 v2/v4 间一致,但模块路径要对齐成 v4,否则类型不匹配(`SetICEUDPMux` 接收的是 ice 包的 `UDPMux` 接口)。

---

## 1. SettingEngine 配置单 UDP 端口(UDPMux)

### 1.1 两种写法

**写法 A(推荐,官方 ice-single-port 示例):用 `ice.NewMultiUDPMuxFromPort`**

它会在「所有网卡」上监听同一个端口,自动按网卡 IP 生成 host 候选,使用最省心:

```go
settingEngine := webrtc.SettingEngine{}

// 监听 UDP 8443,所有 WebRTC 流量都走这个端口
mux, err := ice.NewMultiUDPMuxFromPort(8443)
if err != nil {
    panic(err)
}
settingEngine.SetICEUDPMux(mux)

api := webrtc.NewAPI(webrtc.WithSettingEngine(settingEngine))
```

签名:
```go
func ice.NewMultiUDPMuxFromPort(port int, opts ...UDPMuxFromPortOption) (*MultiUDPMuxDefault, error)
```

可用选项(`UDPMuxFromPortOption`):
- `UDPMuxFromPortWithIPFilter(f func(ip net.IP) bool)` —— 过滤掉不想用的 IP(注意:**没有** `UDPMuxFromPortWithIP`,只有 Filter 版本)
- `UDPMuxFromPortWithInterfaceFilter(f func(string) bool)` —— 过滤网卡(容器里用来排除 docker0/veth)
- `UDPMuxFromPortWithLogger(logger logging.LeveledLogger)`
- `UDPMuxFromPortWithLoopback()` —— 是否带 loopback
- `UDPMuxFromPortWithNet(n transport.Net)`
- `UDPMuxFromPortWithNetworks(networks ...NetworkType)`

**写法 B(更底层,手动绑 `0.0.0.0`):用 `webrtc.NewICEUDPMux`**

适合需要自己掌控 `net.PacketConn`(例如显式绑 `0.0.0.0`、复用已有 socket)的场景:

```go
udpListener, err := net.ListenUDP("udp", &net.UDPAddr{
    IP:   net.IP{0, 0, 0, 0}, // 容器内务必绑 0.0.0.0 而不是某个内网 IP
    Port: 8443,
})
if err != nil {
    panic(err)
}
settingEngine := webrtc.SettingEngine{}
settingEngine.SetICEUDPMux(webrtc.NewICEUDPMux(nil, udpListener))
api := webrtc.NewAPI(webrtc.WithSettingEngine(settingEngine))
```

签名:
```go
func webrtc.NewICEUDPMux(logger logging.LeveledLogger, udpConn net.PacketConn) ice.UDPMux
```
第一个参数 logger 传 `nil` 即用默认。

### 1.2 端口选择与要点

- **UDPMux 必须在创建任何 PeerConnection 之前启动**(文档原话:"UDPMux should be started prior to creating PeerConnections")。
- mux 一次创建,**所有 PeerConnection 共享**(SettingEngine + API 是它们之间共享状态的唯一手段),这正是把多连接收敛到单端口的核心机制。
- 端口随意(示例用 8443),但要选一个 VPS 防火墙/安全组放行、容器端口映射打通的 UDP 端口。
- 写法 B 中绑 `0.0.0.0` 是关键:绑某个具体内网 IP 在容器/多网卡环境会导致候选不可达。绑 `0.0.0.0` 让 socket 接受任意目的 IP 的入包,再配合第 2 节用 `SetNAT1To1IPs` 宣告对外可达 IP。

---

## 2. SetNAT1To1IPs:对外宣告 VPS 公网 IP

VPS / 云主机(EC2 风格)通常是 1:1 NAT:本机网卡只有内网 IP,公网 IP 由云厂商映射。WebRTC 默认只会把内网 IP 写进 host 候选,远端连不上。`SetNAT1To1IPs` 让你把公网 IP 直接塞进候选。

```go
settingEngine := webrtc.SettingEngine{}
settingEngine.SetNAT1To1IPs([]string{"203.0.113.10"}, webrtc.ICECandidateTypeHost)
api := webrtc.NewAPI(webrtc.WithSettingEngine(settingEngine))
```

签名:
```go
func (e *SettingEngine) SetNAT1To1IPs(ips []string, candidateType ICECandidateType)
```

两种模式:
- `webrtc.ICECandidateTypeHost`(本场景推荐):用你给的公网 IP **替换**所有私有 IP 的 host 候选。代价:失去 mDNS / 局域网相关能力(本来就不需要)。
- `webrtc.ICECandidateTypeSrflx`:在原候选基础上**追加**一个 srflx 候选,代价是不能再用公网 STUN。

要点 / 坑:
- `ICECandidateTypeHost` + **私有 IP 不可用**——给的 IP 必须是远端真正可达的(公网)地址,否则会异常。
- 直连公网可达时,Host 模式在 Chrome / Firefox 均工作良好。
- 多网卡(含 docker 虚拟网卡)时建议配合 `SetInterfaceFilter` 排除无关网卡:

```go
settingEngine.SetInterfaceFilter(func(ifName string) bool {
    return !strings.Contains(ifName, "docker") &&
           !strings.Contains(ifName, "veth")
})
settingEngine.SetNAT1To1IPs([]string{publicIP}, webrtc.ICECandidateTypeHost)
```

- 新版本另有更灵活的 `SetICEAddressRewriteRules`(支持 External IP 列表 + AsCandidateType + Mode 如 `ICEAddressRewriteReplace`),需要超出 1:1 NAT 的重写时可用,但常规公网 VPS 用 `SetNAT1To1IPs` 足矣。

---

## 3. 仅音频 Opus 的 track 订阅/转发模式

### 3.1 TrackLocalStaticRTP vs TrackLocalStaticSample

| 类型 | 喂入单位 | 适用 | SFU 选择 |
|---|---|---|---|
| `TrackLocalStaticRTP` | 直接写 **RTP 包**(`WriteRTP(*rtp.Packet)`) | 转发已有 RTP 流,不重编码 | **选它** |
| `TrackLocalStaticSample` | 写**编码后的媒体帧 Sample**(`WriteSample(media.Sample)`,内部自动打包成 RTP) | 自己产生媒体(从磁盘读 Opus 帧、采集编码) | 不用 |

SFU 的本质是「收到 RTP,原样转发给其他人,不解码不重编码」,所以一律用 `TrackLocalStaticRTP`。

创建一个 Opus 转发 track:
```go
trackLocal, err := webrtc.NewTrackLocalStaticRTP(
    webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus},
    "audio", "pion",
)
```

官方 sfu-ws 示例里更稳妥的做法是**直接复用来源 track 的 codec/ID/streamID**,保留原始 Opus 参数:
```go
trackLocal, err := webrtc.NewTrackLocalStaticRTP(
    remote.Codec().RTPCodecCapability, remote.ID(), remote.StreamID(),
)
```

### 3.2 OnTrack:收包 → 写入本地转发 track

```go
peerConnection.OnTrack(func(remote *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
    // 1. 为这条远端 track 创建一个可被其他 PC 订阅的本地 track
    trackLocal := addTrack(remote) // 见 3.3,存入 trackLocals 并触发重协商
    defer removeTrack(trackLocal)

    buf := make([]byte, 1500)
    rtpPkt := &rtp.Packet{}
    for {
        i, _, err := remote.Read(buf)
        if err != nil {
            return
        }
        if err = rtpPkt.Unmarshal(buf[:i]); err != nil {
            return
        }
        rtpPkt.Extension = false        // sfu-ws 示例做法:清掉扩展位避免转发问题
        rtpPkt.Extensions = nil
        if err = trackLocal.WriteRTP(rtpPkt); err != nil {
            return
        }
    }
})
```

### 3.3 多人房间:新成员加入时增删 track + 重协商

核心数据结构(参考 sfu-ws):
- `trackLocals map[string]*webrtc.TrackLocalStaticRTP` —— 房间内所有可转发的本地 track(key=track ID)
- 一份 peer 列表(每个 peer 持有 `*webrtc.PeerConnection` + 信令 websocket)
- 一把全局 `sync.Mutex` 保护上面两者

`signalPeerConnections()`(同步函数,任何 track/peer 变更后调用)做四件事:
1. **清理**:删掉处于 `PeerConnectionStateClosed` 的 peer。
2. **补发缺失 track**:对每个 peer,遍历 `trackLocals`,凡是该 peer 还没在发的(不在它 `Senders()` 里、且不是它自己上行的),`peerConnection.AddTrack(trackLocals[id])`。
3. **移除多余 track**:peer 的某个 sender 对应的 track 已不在 `trackLocals` 里,则 `peerConnection.RemoveTrack(sender)`。
4. **重协商**:`CreateOffer(nil)` → `SetLocalDescription(offer)` → 通过信令把 offer 发给客户端;客户端 answer 回来后 `SetRemoteDescription`。

重协商的并发处理(sfu-ws 关键点):用一个 `attemptSync()` 内循环最多重试 **25 次**;若仍失败(可能正卡在别的 `AddTrack`/`RemoveTrack`),**释放锁,3 秒后再 `signalPeerConnections()`**,避免死锁/竞态:

```go
func signalPeerConnections() {
    listLock.Lock()
    defer func() {
        listLock.Unlock()
        // 失败兜底:稍后再试
    }()

    attemptSync := func() (tryAgain bool) {
        for i := range peerConnections {
            // 1) 清理 closed
            // 2) RemoveTrack 多余的 sender
            // 3) AddTrack 缺失的 trackLocals
            // 4) CreateOffer + SetLocalDescription + 发送 offer
            // 任一步出错则 return true(需重试)
        }
        return false
    }

    for syncAttempt := 0; ; syncAttempt++ {
        if syncAttempt == 25 {
            // 25 次仍未成功 —— 解锁,3 秒后重来
            go func() {
                time.Sleep(time.Second * 3)
                signalPeerConnections()
            }()
            return
        }
        if !attemptSync() {
            break
        }
    }
}
```

### 3.4 OnNegotiationNeeded vs 手动 offer

- **SFU 服务端**:一般**不依赖** `OnNegotiationNeeded`,而是在 `AddTrack/RemoveTrack` 后**手动**走上面的 offer 流程(可控、可加锁、可重试)。服务端是 offerer。
- **客户端(浏览器)**:由 `onnegotiationneeded` 事件触发新 offer/answer(若改成客户端做 offerer 也可以,但 sfu-ws 是服务端 offerer 模式)。
- Pion 原生支持运行中动态增删 track 的重协商(Unified Plan);如果想让 Pion 端在被改动时自动发起,可用 `peerConnection.OnNegotiationNeeded(func(){ ... })`,但与手动 offer 二选一,避免重复协商。官方 `play-from-disk-renegotiation` 示例是「已协商连接动态增删 track」的最佳参考。

---

## 4. 容器内运行时 ICE 候选的坑

容器(Docker/K8s)里 multicast 常被禁、容器在独立 bridge 子网、内网 IP 对外不可达,默认 ICE 行为基本不可用。务必做这几件事:

1. **关闭 mDNS**(否则生成 `.local` 候选,容器里多播失败、信息泄露):
```go
settingEngine.SetICEMulticastDNSMode(ice.MulticastDNSModeDisabled)
```
注意参数是 **`ice.MulticastDNSMode` 枚举**(`ice.MulticastDNSModeDisabled`),不是数字 0。

2. **只暴露 host 候选 + 宣告公网 IP**(见第 2 节):用 `SetNAT1To1IPs([]string{publicIP}, webrtc.ICECandidateTypeHost)` 把候选替换成对外可达 IP,而不是容器内网 IP。

3. **绑 `0.0.0.0` 监听该 UDP 端口**:容器里 socket 必须绑 `0.0.0.0`(写法 B),或用 `NewMultiUDPMuxFromPort` 让它监听所有网卡。**不要**绑某个具体的容器内网 IP——它对外不可达。`0.0.0.0` 只用于「绑定/收包」,不能拿来「宣告」(宣告用第 2 步的公网 IP)。

4. **端口映射对齐**:Docker 必须 `-p 8443:8443/udp`(UDP!),且容器内监听端口 = 映射端口 = `SetNAT1To1IPs` 宣告 IP 上开放的端口。单 UDP 端口 + host-only 候选 + NAT1To1 这一组合天然契合容器一个固定 UDP 端口的部署。

5. **可选:网卡过滤**:`SetInterfaceFilter` 排除 `docker`/`veth`/`br-` 等虚拟网卡,减少无效候选与收集时间。

6. **可选:限制候选网络类型**:只走 UDP host 时可避免无谓的 srflx/relay 收集开销。

> 综合配方(容器/VPS 标准组合):
> 单 UDP 端口 UDPMux + `SetNAT1To1IPs(host)` 宣告公网 IP + 关 mDNS + 绑 0.0.0.0 + Docker UDP 端口映射。

---

## 5. PeerConnection 生命周期 / 清理 / OnTrack 推荐做法

### 5.1 生命周期与清理

- 监听连接状态,**Failed/Closed 时调用 `peerConnection.Close()` 并从房间列表移除,再触发一次 `signalPeerConnections()`**:

```go
peerConnection.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
    switch s {
    case webrtc.PeerConnectionStateFailed:
        _ = peerConnection.Close() // Close 会把状态推进到 Closed
    case webrtc.PeerConnectionStateClosed:
        signalPeerConnections() // 让其余 peer 把这个走掉的人的 track 摘掉
    }
})
```

- `peerConnection.Close()` 释放该连接所有资源(transport、track、收发器),**幂等**,清理路径上多次调用安全。
- 房间最后一人离开时,记得也要清理 `trackLocals` 里属于他的 track 与 mux 上的连接(`MultiUDPMuxDefault.RemoveConnByUfrag(ufrag)` 可按 ufrag 从底层 mux 摘连接)。
- ICE 状态可另用 `OnICEConnectionStateChange` 观测;但生命周期/清理以 `OnConnectionStateChange`(整体 PC 状态)为准更可靠。

### 5.2 OnTrack 推荐做法

- 进 `OnTrack` 第一件事:为该 remote track 建 `TrackLocalStaticRTP`、存入 `trackLocals`、调 `signalPeerConnections()` 让其他人订阅;`defer` 里做 `removeTrack` + 再次 `signalPeerConnections()`。
- RTP 读循环用复用的 `[]byte`(1500)与复用的 `*rtp.Packet`,`Unmarshal` 后 `WriteRTP`;`Read` 返回 error(对端走了)即退出循环。
- 仅音频时通常**不需要**周期性发 PLI/FIR(那是视频关键帧请求);音频可省。
- 所有对 `trackLocals` / peer 列表的读写都要在同一把锁内,避免并发增删 track 时崩。

---

## 6. 最小可用 Go 代码骨架

> 仅音频 Opus、单 UDP 端口、公网 VPS / 容器、多人房间 SFU。信令(WebSocket)与房间路由按需补全;此处聚焦 Pion 部分。

```go
package main

import (
    "net"
    "strings"
    "sync"
    "time"

    "github.com/pion/ice/v4"
    "github.com/pion/rtp"
    "github.com/pion/webrtc/v4"
)

const (
    udpPort  = 8443
    publicIP = "203.0.113.10" // VPS 公网 IP
)

var (
    api         *webrtc.API
    listLock    sync.Mutex
    peers       []peerState                                   // 房间内所有 peer
    trackLocals = map[string]*webrtc.TrackLocalStaticRTP{}    // 可转发的本地 track
)

type peerState struct {
    pc     *webrtc.PeerConnection
    signal func(sdp webrtc.SessionDescription) // 把 offer 发给对应客户端
}

// 1. 初始化共享 API:单 UDP 端口 + 公网 IP + 关 mDNS + 容器友好
func initAPI() {
    se := webrtc.SettingEngine{}

    // (a) 单 UDP 端口 mux(监听所有网卡;容器里等价于绑 0.0.0.0:8443/udp)
    mux, err := ice.NewMultiUDPMuxFromPort(udpPort,
        ice.UDPMuxFromPortWithInterfaceFilter(func(name string) bool {
            return !strings.Contains(name, "docker") &&
                !strings.Contains(name, "veth") &&
                !strings.HasPrefix(name, "br-")
        }),
    )
    if err != nil {
        panic(err)
    }
    se.SetICEUDPMux(mux)

    // (b) 对外宣告公网 IP,候选只用 host
    se.SetNAT1To1IPs([]string{publicIP}, webrtc.ICECandidateTypeHost)

    // (c) 容器内关闭 mDNS,避免 .local 候选
    se.SetICEMulticastDNSMode(ice.MulticastDNSModeDisabled)

    api = webrtc.NewAPI(webrtc.WithSettingEngine(se))
}

// 2. 新成员加入:建 PeerConnection、挂回调
func newPeer(sendOffer func(webrtc.SessionDescription)) (*webrtc.PeerConnection, error) {
    pc, err := api.NewPeerConnection(webrtc.Configuration{})
    if err != nil {
        return nil, err
    }

    // 仅音频:声明要收发音频
    if _, err = pc.AddTransceiverFromKind(webrtc.RTPCodecTypeAudio,
        webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionSendrecv},
    ); err != nil {
        return nil, err
    }

    // 生命周期清理
    pc.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
        switch s {
        case webrtc.PeerConnectionStateFailed:
            _ = pc.Close()
        case webrtc.PeerConnectionStateClosed:
            signalPeerConnections()
        }
    })

    // 收到上行音频 -> 建本地转发 track -> 转发 RTP
    pc.OnTrack(func(remote *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
        local := addTrack(remote)
        defer removeTrack(local)

        buf := make([]byte, 1500)
        pkt := &rtp.Packet{}
        for {
            n, _, err := remote.Read(buf)
            if err != nil {
                return
            }
            if err = pkt.Unmarshal(buf[:n]); err != nil {
                return
            }
            pkt.Extension = false
            pkt.Extensions = nil
            if err = local.WriteRTP(pkt); err != nil {
                return
            }
        }
    })

    listLock.Lock()
    peers = append(peers, peerState{pc: pc, signal: sendOffer})
    listLock.Unlock()

    signalPeerConnections()
    return pc, nil
}

func addTrack(remote *webrtc.TrackRemote) *webrtc.TrackLocalStaticRTP {
    listLock.Lock()
    defer func() {
        listLock.Unlock()
        signalPeerConnections()
    }()
    local, err := webrtc.NewTrackLocalStaticRTP(
        remote.Codec().RTPCodecCapability, remote.ID(), remote.StreamID(),
    )
    if err != nil {
        panic(err)
    }
    trackLocals[remote.ID()] = local
    return local
}

func removeTrack(t *webrtc.TrackLocalStaticRTP) {
    listLock.Lock()
    defer func() {
        listLock.Unlock()
        signalPeerConnections()
    }()
    delete(trackLocals, t.ID())
}

// 3. 同步所有 PeerConnection 的 track 并重协商(服务端 offerer)
func signalPeerConnections() {
    listLock.Lock()
    defer listLock.Unlock()

    attemptSync := func() (tryAgain bool) {
        // 清理已关闭的 peer
        kept := peers[:0]
        for _, p := range peers {
            if p.pc.ConnectionState() == webrtc.PeerConnectionStateClosed {
                continue
            }
            kept = append(kept, p)
        }
        peers = kept

        for _, p := range peers {
            // 已存在的 sender
            existing := map[string]bool{}
            for _, sender := range p.pc.GetSenders() {
                if sender.Track() == nil {
                    continue
                }
                id := sender.Track().ID()
                // 移除已不在房间里的 track
                if _, ok := trackLocals[id]; !ok {
                    if err := p.pc.RemoveTrack(sender); err != nil {
                        return true
                    }
                } else {
                    existing[id] = true
                }
            }
            // 不要把自己上行的 track 再回发给自己
            for _, recv := range p.pc.GetReceivers() {
                if recv.Track() != nil {
                    existing[recv.Track().ID()] = true
                }
            }
            // 补发缺失的 track
            for id, local := range trackLocals {
                if existing[id] {
                    continue
                }
                if _, err := p.pc.AddTrack(local); err != nil {
                    return true
                }
            }
            // 重协商:服务端生成 offer 并下发
            offer, err := p.pc.CreateOffer(nil)
            if err != nil {
                return true
            }
            if err = p.pc.SetLocalDescription(offer); err != nil {
                return true
            }
            p.signal(offer) // 通过信令把 offer 发给客户端;客户端 answer 回来后 SetRemoteDescription
        }
        return false
    }

    for attempt := 0; ; attempt++ {
        if attempt == 25 {
            go func() {
                time.Sleep(3 * time.Second)
                signalPeerConnections()
            }()
            return
        }
        if !attemptSync() {
            return
        }
    }
}

func main() {
    initAPI()
    // TODO: 起 WebSocket 信令服务,处理 newPeer / SetRemoteDescription(answer) / AddICECandidate
    select {}
}
```

> 说明:`signalPeerConnections` 里递归调用通过 `go func(){...}` 延迟触发,避免与当前持锁路径冲突;真实工程里建议把「锁」与「重试」拆得更细,并对 `SetRemoteDescription(answer)` 做单独的信令处理。客户端需用 Unified Plan、回 answer、并 Trickle ICE 互发候选。

---

## [参考链接]

- Pion WebRTC v4 API 文档: https://pkg.go.dev/github.com/pion/webrtc/v4
- ice-single-port 官方示例(NewMultiUDPMuxFromPort / SetICEUDPMux): https://github.com/pion/webrtc/tree/master/examples/ice-single-port
- SettingEngine 源码(SetICEUDPMux / SetNAT1To1IPs / SetICEMulticastDNSMode / SetInterfaceFilter): https://github.com/pion/webrtc/blob/main/settingengine.go
- sfu-ws 官方 SFU 示例(OnTrack / TrackLocalStaticRTP / signalPeerConnections / 生命周期): https://github.com/pion/example-webrtc-applications/blob/master/sfu-ws/main.go
- pion/example-webrtc-applications 仓库(SFU/RTP-forwarder/broadcast): https://github.com/pion/example-webrtc-applications
- pion/ice UDPMux 源码与选项(NewMultiUDPMuxFromPort / UDPMuxFromPortOption): https://github.com/pion/ice/blob/main/udp_mux_multi.go
- pion/ice udp_mux.go: https://github.com/pion/ice/blob/master/udp_mux.go
- pion/mdns(mDNS 实现,对应 SetICEMulticastDNSMode): https://github.com/pion/mdns
- ICE & Connectivity 概念文档(NAT1To1 / host vs srflx): https://mintlify.wiki/pion/webrtc/concepts/ice-and-connectivity
- 1:1 NAT 支持 issue(SetNAT1To1IPs 设计背景,对标 Janus --nat-1-1): https://github.com/pion/webrtc/issues/835
- 单端口模式讨论 issue: https://github.com/pion/webrtc/issues/639
- v4.2.0 发布说明(2025 收官版): https://github.com/pion/webrtc/releases/tag/v4.2.0
- pion/webrtc Releases(确认 v4.2.5 / CVE-2026-26014): https://github.com/pion/webrtc/releases
- 社区 SFU 教程(房间结构与 trackLocals map / Trickle ICE): https://dev.to/vthesaint/dive-into-web-rtc-or-write-sfu-on-your-own-1461
