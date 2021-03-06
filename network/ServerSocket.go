package network

import (
	"net"
	"sync"
	"sync/atomic"

	"github.com/snowyyj001/loumiao/llog"
)

type IServerSocket interface {
	ISocket

	AssignClientId() int
	GetClientById(int) *ServerSocketClient
	LoadClient() *ServerSocketClient
	AddClinet(*net.TCPConn, string, int) *ServerSocketClient
	DelClinet(*ServerSocketClient) bool
	StopClient(int)
	ClientRemoteAddr(clientid int) string
}

type ServerSocket struct {
	Socket
	m_nClientCount  int
	m_nMaxClients   int
	m_nMinClients   int
	m_nIdSeed       int32
	m_bShuttingDown bool
	m_ClientList    map[int]*ServerSocketClient
	m_ClientLocker  *sync.RWMutex
	m_Listen        *net.TCPListener
	m_Lock          sync.Mutex
}

func (self *ServerSocket) Init(saddr string) bool {
	self.Socket.Init(saddr)
	self.m_ClientList = make(map[int]*ServerSocketClient)
	self.m_ClientLocker = &sync.RWMutex{}
	return true
}
func (self *ServerSocket) Start() bool {
	if self.m_nConnectType == 0 {
		llog.Error("ServerSocket.Start error : unkonwen socket type")
		return false
	}
	self.m_bShuttingDown = false

	if self.m_sAddr == "" {
		llog.Error("ServerSocket Start error, saddr is null")
		return false
	}

	tcpAddr, err := net.ResolveTCPAddr("tcp4", self.m_sAddr)
	if err != nil {
		llog.Errorf("%v", err)
	}
	ln, err := net.ListenTCP("tcp4", tcpAddr)
	if err != nil {
		llog.Errorf("%v", err)
		return false
	}

	llog.Infof("ServerSocket 启动监听，等待链接！%s", self.m_sAddr)
	self.m_Listen = ln
	//延迟，监听关闭
	//defer ln.Close()
	self.m_nState = SSF_ACCEPT
	go serverRoutine(self)
	return true
}

func (self *ServerSocket) AssignClientId() int {
	return int(atomic.AddInt32(&self.m_nIdSeed, 1))
}

func (self *ServerSocket) GetClientById(id int) *ServerSocketClient {
	self.m_ClientLocker.RLock()
	client, exist := self.m_ClientList[id]
	self.m_ClientLocker.RUnlock()
	if exist == true {
		return client
	}

	return nil
}

func (self *ServerSocket) ClientRemoteAddr(clientid int) string {
	pClinet := self.GetClientById(clientid)
	if pClinet != nil {
		return pClinet.m_Conn.RemoteAddr().String()
	}
	return ""
}

func (self *ServerSocket) AddClinet(tcpConn *net.TCPConn, addr string, connectType int) *ServerSocketClient {
	pClient := self.LoadClient()
	if pClient != nil {
		pClient.Socket.Init(addr)
		pClient.m_pServer = self
		pClient.m_ClientId = self.AssignClientId()
		pClient.SetConnectType(connectType)
		pClient.SetTcpConn(tcpConn)
		pClient.BindPacketFunc(self.m_PacketFunc)
		self.m_ClientLocker.Lock()
		self.m_ClientList[pClient.m_ClientId] = pClient
		self.m_ClientLocker.Unlock()
		pClient.Start()
		self.m_nClientCount++
		llog.Debugf("客户端：%s已连接[%d]", tcpConn.RemoteAddr().String(), pClient.m_ClientId)
		return pClient
	} else {
		tcpConn.Close()
		llog.Errorf("ServerSocket.AddClinet %s", "无法创建客户端连接对象")
	}
	return nil
}

func (self *ServerSocket) DelClinet(pClient *ServerSocketClient) bool {
	self.m_ClientLocker.Lock()
	delete(self.m_ClientList, pClient.m_ClientId)
	llog.Debugf("客户端：%s已断开连接[%d]", pClient.m_Conn.RemoteAddr().String(), pClient.m_ClientId)
	self.m_ClientLocker.Unlock()
	self.m_nClientCount--
	return true
}

func (self *ServerSocket) StopClient(id int) {
	pClinet := self.GetClientById(id)
	if pClinet != nil {
		pClinet.Close()
	}
}

func (self *ServerSocket) LoadClient() *ServerSocketClient {
	s := &ServerSocketClient{}
	s.m_MaxReceiveBufferSize = self.m_MaxReceiveBufferSize
	s.m_MaxSendBufferSize = self.m_MaxSendBufferSize
	return s
}

func (self *ServerSocket) SendById(id int, buff []byte) int {
	pClient := self.GetClientById(id)
	if pClient != nil {
		pClient.Send(buff)
	} else {
		llog.Warningf("ServerSocket发送数据失败[%d]", id)
	}
	return 0
}

func (self *ServerSocket) BroadCast(buff []byte) {
	self.m_ClientLocker.RLock()
	for _, client := range self.m_ClientList {
		client.Send(buff)
	}
	self.m_ClientLocker.Unlock()
}

func (self *ServerSocket) Restart() bool {
	return true
}

func (self *ServerSocket) Connect() bool {
	return true
}

func (self *ServerSocket) Disconnect(bool) bool {
	return true
}

func (self *ServerSocket) OnNetFail(int) {
}

func (self *ServerSocket) Close() {
	self.m_Listen.Close()
	self.Clear()

}

func (self *ServerSocket) SetMaxClients(maxnum int) {
	self.m_nMaxClients = maxnum
}

func serverRoutine(server *ServerSocket) {
	for {
		tcpConn, err := server.m_Listen.AcceptTCP()
		if err != nil {
			llog.Errorf("ServerScoket serverRoutine listen err: %s", err.Error())
			break
		}

		if server.m_nClientCount >= server.m_nMaxClients {
			tcpConn.Close()
			llog.Warning("serverRoutine: too many conns")
			continue
		}

		handleConn(server, tcpConn, tcpConn.RemoteAddr().String())
	}
	server.Close()
}

func handleConn(server *ServerSocket, tcpConn *net.TCPConn, addr string) bool {
	if tcpConn == nil {
		return false
	}

	pClient := server.AddClinet(tcpConn, addr, server.m_nConnectType)
	if pClient == nil {
		return false
	}

	return true
}
