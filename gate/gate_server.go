// 网关服务
package gate

import (
	"encoding/json"
	"fmt"
	"github.com/snowyyj001/loumiao/message"
	"strings"
	"sync"

	"github.com/snowyyj001/loumiao/lnats"

	"github.com/snowyyj001/loumiao/config"
	"github.com/snowyyj001/loumiao/define"
	"github.com/snowyyj001/loumiao/etcd"
	"github.com/snowyyj001/loumiao/gorpc"
	"github.com/snowyyj001/loumiao/llog"
	"github.com/snowyyj001/loumiao/network"
	"github.com/snowyyj001/loumiao/nodemgr"
	"github.com/snowyyj001/loumiao/util"
)

var (
	This        *GateServer
	handler_Map map[string]string
)

type Token struct {
	UserId  int //这里应该是int64，所以，程序只能在64位机上运行，否则会有异常
	TokenId int
}

/*tokens, tokens_u, users_u说明
当loumiao是account时:
tokens: 没有用
tokens_u: 没有用
================================================================================
当loumiao是gate时:
tokens：key是client的socketid，Token.UserId是client的userid， Token.TokenId是client的tokenid
tokens_u：key是client的userid,value是socketid
users_u: key是client的userid,value是world的uid
================================================================================
当loumiao是server时:
tokens：key是gate的socketid，Token.UserId是gate的uid， Token.TokenId是自己的uid(0代表该gate将要被关闭)
tokens_u：key是gate的uid,value是socketid
users_u: key是client的userid,value是gate的uid,在收到LouMiaoNetMsg时绑定（意味着如果没有事先收到cleint的消息包，server是不知道它所归属的gate的，除非主动BindGate）
*/

type GateServer struct {
	gorpc.GoRoutineLogic

	Id            int
	pService      network.ISocket
	clients       map[int]*network.ClientSocket
	pInnerService network.ISocket
	ServerType    int
	//
	tokens     map[int]*Token
	tokens_u   map[int]int
	users_u    map[int]int
	OnlineNum  int
	clientEtcd *etcd.ClientDis
	rpcMap     map[string][]int
	rpcGates   []int //space for time

	m_etcdKey string

	lock sync.Mutex

	InitFunc func() //需要额外处理的函数回调
}

func (self *GateServer) DoInit() bool {
	llog.Infof("%s DoInit", self.Name)
	This = self

	if self.ServerType == network.CLIENT_CONNECT { //对外(login,gate)
		if config.NET_WEBSOCKET {
			self.pService = new(network.WebSocket)
			self.pService.(*network.WebSocket).SetMaxClients(config.NET_MAX_CONNS)
		} else {
			self.pService = new(network.ServerSocket)
			self.pService.(*network.ServerSocket).SetMaxClients(config.NET_MAX_CONNS)
		}
		self.pService.Init(config.NET_LISTEN_SADDR)
		self.pService.BindPacketFunc(packetFunc)
		self.pService.SetConnectType(network.CLIENT_CONNECT)
	}

	if self.ServerType == network.CLIENT_CONNECT { //对外(login,gate)
		self.clients = make(map[int]*network.ClientSocket)
	} else {
		self.pInnerService = new(network.ServerSocket)
		self.pInnerService.(*network.ServerSocket).SetMaxClients(config.NET_MAX_RPC_CONNS)
		self.pInnerService.Init(config.NET_LISTEN_SADDR)
		self.pInnerService.BindPacketFunc(packetFunc)
		self.pInnerService.SetConnectType(network.SERVER_CONNECT)
	}

	self.tokens = make(map[int]*Token)
	self.tokens_u = make(map[int]int)
	self.users_u = make(map[int]int)
	self.rpcMap = make(map[string][]int) //base64(funcname) -> [uid,uid,...]

	handler_Map = make(map[string]string)

	if self.InitFunc != nil {
		self.InitFunc()
	}

	return true
}

func (self *GateServer) DoRegsiter() {
	llog.Info("GateServer DoRegsiter")

	self.Register("RegisterNet", registerNet)
	self.Register("UnRegisterNet", unRegisterNet)
	self.Register("SendClient", sendClient)
	self.Register("SendMulClient", sendMulClient)
	self.Register("NewRpc", newRpc)
	self.Register("SendRpc", sendRpc)
	self.Register("BroadCastRpc", broadCastRpc)
	self.Register("SendGate", sendGate)
	self.Register("RecvPackMsg", recvPackMsg)
	self.Register("ReportOnLineNum", reportOnLineNum)
	self.Register("CloseServer", closeServer)
	self.Register("BindGate", bindGate)

	//equal to RegisterSelfNet
	handler_Map["CONNECT"] = "GateServer" //in gate, client connect with gate, in server, gate(as client) connect with server
	self.RegisterGate("CONNECT", innerConnect)

	handler_Map["DISCONNECT"] = "GateServer"
	self.RegisterGate("DISCONNECT", innerDisConnect)

	handler_Map["C_CONNECT"] = "GateServer" //in gate, gate connect with server, no in server
	self.RegisterGate("C_CONNECT", outerConnect)

	handler_Map["C_DISCONNECT"] = "GateServer"
	self.RegisterGate("C_DISCONNECT", outerDisConnect)

	handler_Map["LouMiaoLoginGate"] = "GateServer"
	self.RegisterGate("LouMiaoLoginGate", innerLouMiaoLoginGate)

	handler_Map["LouMiaoRpcRegister"] = "GateServer"
	self.RegisterGate("LouMiaoRpcRegister", innerLouMiaoRpcRegister)

	handler_Map["LouMiaoRpcMsg"] = "GateServer"
	self.RegisterGate("LouMiaoRpcMsg", innerLouMiaoRpcMsg)

	handler_Map["LouMiaoBroadCastMsg"] = "GateServer"
	self.RegisterGate("LouMiaoBroadCastMsg", innerLouMiaoBroadCastMsg)

	handler_Map["LouMiaoNetMsg"] = "GateServer"
	self.RegisterGate("LouMiaoNetMsg", innerLouMiaoNetMsg)

	handler_Map["LouMiaoKickOut"] = "GateServer"
	self.RegisterGate("LouMiaoKickOut", innerLouMiaoKickOut)

	handler_Map["LouMiaoClientConnect"] = "GateServer"
	self.RegisterGate("LouMiaoClientConnect", innerLouMiaoClientConnect)

	handler_Map["LouMiaoBindGate"] = "GateServer"
	self.RegisterGate("LouMiaoBindGate", innerLouMiaoLouMiaoBindGate)
}

//begin communicate with other nodes
func (self *GateServer) DoStart() {
	llog.Info("GateServer DoStart")

	//etcd client
	client, err := etcd.NewClientDis(config.Cfg.EtcdAddr)
	if util.CheckErr(err) {
		llog.Fatalf("etcd connect failed: %v", config.Cfg.EtcdAddr)
	}
	self.Id = config.Cfg.NetCfg.Uid
	self.m_etcdKey = fmt.Sprintf("%s%s", define.ETCD_NODEINFO, config.NET_GATE_SADDR)
	self.clientEtcd = client
	self.clientEtcd.SetLeasefunc(leaseCallBack)
	self.clientEtcd.SetLease(int64(config.GAME_LEASE_TIME), true)

	//server discover
	if self.ServerType == network.CLIENT_CONNECT { //account/gate watch server
		//watch status, for balance
		err = self.clientEtcd.WatchStatusList(define.ETCD_NODESTATUS, nodemgr.NodeStatusUpdate)
		if err != nil {
			llog.Fatalf("etcd watch ETCD_NODESTATUS error : %s", err.Error())
		}
		//watch all node, just for account, to gate balance
		_, err = self.clientEtcd.WatchNodeList(define.ETCD_NODEINFO, self.newServerDiscover)
		if err != nil {
			llog.Fatalf("etcd watch NET_GATE_SADDR error : %s", err.Error())
		}
	} else { //for simple, only login and gate need server infos, others should goto gate for query
		if config.NET_NODE_TYPE == config.ServerType_World { //need know the zone's state
			_, err = self.clientEtcd.WatchNodeList(define.ETCD_NODEINFO, self.newServerDiscover)
			if err != nil {
				llog.Fatalf("etcd watch NET_GATE_SADDR error : %s", err.Error())
			}
		}
	}

	llog.Infof("GateServer DoStart success: name=%s,saddr=%s,uid=%d", self.Name, config.NET_GATE_SADDR, self.Id)
}

//begin start socket servie
func (self *GateServer) DoOpen() {
	if self.pService != nil {
		if self.pService.Start() == false {
			util.Assert(nil)
		}
	}
	if self.pInnerService != nil {
		if self.pInnerService.Start() == false {
			util.Assert(nil)
		}
	}
	//register to etcd when the socket is ok
	obj, _ := json.Marshal(&config.Cfg.NetCfg)
	if err := self.clientEtcd.PutService(self.m_etcdKey, string(obj)); err != nil {
		llog.Fatalf("etcd PutService error %v", err)
	}

	nodemgr.SocketActive = true

	llog.Infof("GateServer DoOpen success: name=%s,saddr=%s,uid=%d", self.Name, config.NET_GATE_SADDR, self.Id)

	reqParam := &struct {
		Tag     int    `json:"tag"`     //邮件类型
		Id      int    `json:"id"`      //区服id
		Content string `json:"content"` //邮件内容
	}{}
	reqParam.Tag = define.MAIL_TYPE_START
	reqParam.Id = config.NET_NODE_ID
	reqParam.Content = fmt.Sprintf("uid: %d \nname: %s\nhost: %s\n服务器完成启动", config.SERVER_NODE_UID, config.SERVER_NAME, config.NET_GATE_SADDR)
	buffer, err := json.Marshal(&reqParam)
	if err == nil {
		lnats.Publish(define.TOPIC_SERVER_MAIL, buffer)
	}
}

//simple register self net hanlder, this func can only be called before igo started
func (self *GateServer) RegisterSelfNet(hanlderName string, hanlderFunc gorpc.HanlderNetFunc) {
	if self.IsRunning() {
		llog.Fatal("RegisterSelfNet error, igo has already started")
		return
	}
	handler_Map[hanlderName] = "GateServer"
	self.RegisterGate(hanlderName, hanlderFunc)
}

//goroutine unsafe
func (self *GateServer) newServerDiscover(key, val string, dis bool) {
	arrStr := strings.Split(key, "/")
	if len(arrStr) < 3 {
		llog.Errorf("newServerDiscover error fromat key : %s", key)
		return
	}
	saddr := arrStr[2]
	llog.Debugf("newServerDiscover: key=%s,val=%s,dis=%t,debug=%s", key, val, dis, saddr)

	if dis == true {
		node := nodemgr.GetNodeByAddr(saddr)
		if node != nil { //maybe, etcd still have older data, etcd has a huge delay
			llog.Warningf("newServerDiscover: saddr=%s,old server=%v,new server=%s", saddr, node, val)
			nodemgr.RemoveNode(saddr)
		}
		node = &nodemgr.NodeInfo{}
		json.Unmarshal([]byte(val), node)
		node.SocketActive = true
		nodemgr.AddNode(node)

		if config.NET_NODE_TYPE != config.ServerType_Gate {
			return
		}
		if node.Type == config.ServerType_Gate || node.Type == config.ServerType_Account { //filter gate and account
			return
		}
		/*if config.NET_NODE_TYPE == config.ServerType_Account {
			if node.Type != config.ServerType_World { //Account only connect to world,just for using the socket state, knowing the world is closed in time
				return
			}
		}*/
		rpcClient := self.GetRpcClient(node.Uid) //this conditation can be etcd reconnect
		if rpcClient == nil {
			client := self.buildRpc(node.Uid, saddr)
			if self.enableRpcClient(client) {
				m := &gorpc.M{Id: node.Uid, Data: client}
				gorpc.MGR.Send("GateServer", "NewRpc", m)
			}
		}
	} else {
		nodemgr.RemoveNode(saddr)
	}
}

func (self *GateServer) enableRpcClient(client *network.ClientSocket) bool {
	if client.Start() {
		llog.Infof("GateServer rpc connected %s success", client.GetSAddr())
		return true
	} else {
		llog.Warningf("GateServer rpc connect failed %s", client.GetSAddr())
		return false
	}
	return true
}

func (self *GateServer) DoDestory() {
	nodemgr.SocketActive = false
	if self.clientEtcd != nil {
		self.clientEtcd.RevokeLease()
	}
}

//goroutine unsafe
//net msg handler,this func belong to socket's goroutine
func packetFunc(socketid int, buff []byte, nlen int) bool {
	//llog.Debugf("packetFunc: socketid=%d, bufferlen=%d", socketid, nlen)
	err, target, name, pm := message.Decode(This.Id, buff, nlen)
	//llog.Debugf("packetFunc %d %s %v", target, name, pm)
	if nil != err {
		llog.Errorf("packetFunc Decode error: %s", err.Error())
		//This.closeClient(socketid)
	} else {
		if target == This.Id || target <= 0 { //msg to me
			handler, ok := handler_Map[name]
			if ok {
				nm := &gorpc.M{Id: socketid, Name: name, Data: pm}
				gorpc.MGR.Send(handler, "ServiceHandler", nm)
			} else {
				llog.Errorf("packetFunc handler is nil, drop it[%s]", name)
			}
		} else { //msg to other server
			//newbuff := make([]byte, nlen)
			//copy(newbuff, buff[:nlen])
			newbuff := message.GetBuffer(nlen)
			copy(newbuff, buff[:nlen])
			m := &gorpc.M{Id: socketid, Param: target, Data: newbuff}
			gorpc.MGR.Send("GateServer", "RecvPackMsg", m)
		}
	}
	return true
}

func (self *GateServer) buildRpc(uid int, addr string) *network.ClientSocket {
	client := new(network.ClientSocket)
	client.SetClientId(uid)
	client.Init(addr)
	client.SetConnectType(network.CHILD_CONNECT)
	client.BindPacketFunc(packetFunc)
	client.Uid = uid

	return client
}

func (self *GateServer) removeRpc(uid int) {
	_, ok := self.clients[uid]
	if !ok {
		return
	}
	llog.Debugf("GateServer removeRpc: %d", uid)

	delete(self.clients, uid)

	//remove rpc handler
	self.removeRpcHanlder(uid)

	//reset node ststus
	nodemgr.DisableNode(uid)
}

func (self *GateServer) removeRpcHanlder(uid int) {
	//remove rpc handler
	for key, arr := range self.rpcMap {
		for i, val := range arr {
			if val == uid {
				self.rpcMap[key] = append(arr[:i], arr[i+1:]...)
				break
			}
		}
	}
}

func (self *GateServer) GetRpcClient(uid int) *network.ClientSocket {
	client, ok := self.clients[uid]
	if ok {
		return client
	}
	return nil
}

//rpc调用的gate选择
func (self *GateServer) getCluserGateClientId() int {
	if self.ServerType == network.CLIENT_CONNECT {
		llog.Fatalf("getCluserGate: gate or login can not call this func")
		return 0
	} else {
		sz := len(self.rpcGates)
		if sz == 0 {
			llog.Warning("0.getCluserGateClientId no gate server finded ")
			return 0
		} else {
			index := util.Random(sz) //choose a gate by random
			return self.rpcGates[index]
		}
	}
	return 0
}

//rpc调用的目标server选择
func (self *GateServer) getCluserServer(funcName string) *network.ClientSocket {
	arr := self.rpcMap[funcName]
	sz := len(arr)
	if sz == 0 {
		llog.Warningf("0.getCluserServerUid no rpc server hanlder finded %s", funcName)
		return nil
	}
	index := util.Random(sz) //choose a server by random
	uid := arr[index]
	client, _ := self.clients[uid]
	return client
}

func (self *GateServer) StopClient(userId int) {
	sid := This.tokens_u[userId]
	if sid > 0 {
		self.closeClient(sid)
	}
}

func (self *GateServer) closeClient(clientid int) {
	if self.ServerType == network.CLIENT_CONNECT {
		if config.NET_WEBSOCKET {
			self.pService.(*network.WebSocket).StopClient(clientid)
		} else {
			self.pService.(*network.ServerSocket).StopClient(clientid)
		}
	} else {
		self.pInnerService.(*network.ServerSocket).StopClient(clientid)
	}
}

// 向内部server直接发送buffer消息,专为gate使用，
// 必须保证线程安全，即需要在gateserver的igo中调用该函数
func (self *GateServer) SendServer(target int, buff []byte) {
	rpcClient := self.GetRpcClient(target)
	if rpcClient != nil {
		rpcClient.Send(buff)
	} else {
		llog.Errorf("GateServer.SendServer target error: target=%d", target)
	}
}
