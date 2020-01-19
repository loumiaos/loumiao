package network

import (
	"fmt"
	"github.com/snowyyj001/loumiao/log"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/gorilla/websocket"
)

type IWebSocket interface {
	ISocket

	AssignClientId() int
	GetClientById(int) *WebSocketClient
	LoadClient() *WebSocketClient
	AddClinet(*websocket.Conn, string, int) *WebSocketClient
	DelClinet(*WebSocketClient) bool
	StopClient(int)
}

type WebSocket struct {
	Socket
	m_nClientCount  int
	m_nMaxClients   int
	m_nMinClients   int
	m_nIdSeed       int32
	m_bShuttingDown bool
	m_bCanAccept    bool
	m_bNagle        bool
	m_ClientList    map[int]*WebSocketClient
	m_ClientLocker  *sync.RWMutex
	m_Pool          sync.Pool
	m_Lock          sync.Mutex
}

var upgrader = websocket.Upgrader{} // use default options
var This *WebSocket

func (self *WebSocket) Init(ip string, port int) bool {
	This = self
	self.Socket.Init(ip, port)
	self.m_ClientList = make(map[int]*WebSocketClient)
	self.m_ClientLocker = &sync.RWMutex{}
	self.m_sIP = ip
	self.m_nPort = port
	self.m_Pool = sync.Pool{
		New: func() interface{} {
			var s = &WebSocketClient{}
			return s
		},
	}
	return true
}
func (self *WebSocket) Start() bool {
	self.m_bShuttingDown = false

	if self.m_sIP == "" {
		self.m_sIP = "127.0.0.1"
	}

	var strRemote = fmt.Sprintf("%s:%d", self.m_sIP, self.m_nPort)
	http.HandleFunc("/", serveHome)
	http.HandleFunc("/ws", serveWs)
	go func() {
		err := http.ListenAndServe(strRemote, nil)
		if err != nil {
			log.Errorf("WebSocket ListenAndServe: %v", err)
			return
		}
	}()
	log.Infof("启动监听，等待链接！%s", strRemote)

	//延迟，监听关闭
	//defer ln.Close()
	self.m_nState = SSF_ACCEPT
	return true
}

func (self *WebSocket) AssignClientId() int {
	return int(atomic.AddInt32(&self.m_nIdSeed, 1))
}

func (self *WebSocket) GetClientById(id int) *WebSocketClient {
	self.m_ClientLocker.RLock()
	client, exist := self.m_ClientList[id]
	self.m_ClientLocker.RUnlock()
	if exist == true {
		return client
	}

	return nil
}

func (self *WebSocket) AddClinet(wConn *websocket.Conn, addr string, connectType int) *WebSocketClient {
	pClient := self.LoadClient()
	if pClient != nil {
		pClient.Socket.Init(addr, 0)
		pClient.m_pServer = self
		pClient.m_ClientId = self.AssignClientId()
		pClient.SetConnectType(connectType)
		pClient.SetWsConn(wConn)
		pClient.BindPacketFunc(self.m_PacketFunc)
		self.m_ClientLocker.Lock()
		self.m_ClientList[pClient.m_ClientId] = pClient
		self.m_ClientLocker.Unlock()
		pClient.Start()
		self.m_nClientCount++
		log.Debugf("客户端：%s已连接[%d]！\n", wConn.RemoteAddr().String(), pClient.m_ClientId)
		return pClient
	} else {
		log.Errorf("%s", "无法创建客户端连接对象")
	}
	return nil
}

func (self *WebSocket) DelClinet(pClient *WebSocketClient) bool {
	self.m_Pool.Put(pClient)
	self.m_ClientLocker.Lock()
	delete(self.m_ClientList, pClient.m_ClientId)
	log.Debugf("客户端：已断开连接[%d]！\n", pClient.m_ClientId)
	self.m_ClientLocker.Unlock()
	self.m_nClientCount--
	return true
}

func (self *WebSocket) StopClient(id int) {
	pClinet := self.GetClientById(id)
	if pClinet != nil {
		pClinet.Stop()
	}
}

func (self *WebSocket) LoadClient() *WebSocketClient {
	s := self.m_Pool.Get().(*WebSocketClient)
	s.m_MaxReceiveBufferSize = self.m_MaxReceiveBufferSize
	s.m_MaxSendBufferSize = self.m_MaxSendBufferSize
	return s
}

func (self *WebSocket) Stop() bool {
	if self.m_bShuttingDown {
		return true
	}

	self.m_bShuttingDown = true
	self.m_nState = SSF_SHUT_DOWN
	return true
}

func (self *WebSocket) SendById(id int, buff []byte) int {
	pClient := self.GetClientById(id)
	if pClient != nil {
		pClient.Send(buff)
	} else {
		log.Warningf("ServerSocket发送数据失败[%d]", id)
	}
	return 0
}

func (self *WebSocket) Restart() bool {
	return true
}

func (self *WebSocket) Connect() bool {
	return true
}

func (self *WebSocket) Disconnect(bool) bool {
	return true
}

func (self *WebSocket) OnNetFail(int) {
}

func (self *WebSocket) Close() {
	self.Clear()
	//self.m_Pool.Put(self)
}

func (self *WebSocket) SetMaxClients(maxnum int) {
	self.m_nMaxClients = maxnum
}

func serveWs(w http.ResponseWriter, r *http.Request) {
	log.Infof("客户端：%s已连接！\n", r.RemoteAddr)
	c, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Errorf("serveWs upgrade:", err)
		return
	}
	pClient := This.AddClinet(c, r.RemoteAddr, This.m_nConnectType)
	pClient.Start()
}

func serveHome(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "Not alowed", http.StatusNotFound)
}