package loumiao

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/snowyyj001/loumiao/gorpc"
	"github.com/snowyyj001/loumiao/log"
	"github.com/snowyyj001/loumiao/message"
)

//创建一个服务，稍后开启
func Prepare(igo gorpc.IGoRoutine, name string, sync bool) {
	igo.SetSync(sync)
	gorpc.GetGoRoutineMgr().Start(igo, name)
}

//创建一个服务,立即开启
func Start(igo gorpc.IGoRoutine, name string, sync bool) {
	Prepare(igo, name, sync)
	gorpc.GetGoRoutineMgr().DoSingleStart(name)
}

//开启游戏
func Run() {

	message.DoInit()

	gorpc.GetGoRoutineMgr().DoStart()

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, os.Kill, syscall.SIGTERM)
	sig := <-c
	log.Infof("loumiao closing down (signal: %v)", sig)

	gorpc.GetGoRoutineMgr().CloseAll()

	log.Infof("loumiao done !")
}

//向网关注册网络消息
func RegisterNetHandler(igo gorpc.IGoRoutine, name string, call gorpc.HanlderNetFunc) {
	igo.Register("NetRpC", gorpc.NetRpC)
	igo.Send("GateServer", "RegisterNet", gorpc.M{"name": name, "receiver": igo.GetName()})
	igo.RegisterGate(name, call)
}

func UnRegisterNetHandler(igo gorpc.IGoRoutine, name string) {
	igo.Send("GateServer", "UnRegisterNet", gorpc.M{"name": name})
	igo.UnRegisterGate(name)
}

//发送给客户端消息
func SendClient(clientid int, data interface{}) {
	server := gorpc.GetGoRoutineMgr().GetRoutine("GateServer")
	job := gorpc.ChannelContext{"SendClient", gorpc.SimpleNet(clientid, "", data), nil, nil}
	server.GetJobChan() <- job
}
