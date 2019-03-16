package player

import (
	"gonet/actor"
	"gonet/db"
	"fmt"
	"gonet/base"
	"strings"
	"database/sql"
	"gonet/message"
	"gonet/server/world"
	"sync"
	"github.com/golang/protobuf/proto"
)
//********************************************************
// 玩家管理
//********************************************************
var(
	PLAYERMGR PlayerMgr
	PLAYER Player
)
type(
	PlayerMgr struct{
		actor.Actor
		m_PlayerMap map[int64] *Player
		m_db *sql.DB
		m_Log *base.CLog
		m_Lock *sync.RWMutex
	}

	IPlayerMgr interface {
		actor.IActor

		GetPlayer(accountId int64) *Player
		AddPlayer(accountId int64) *Player
		RemovePlayer(accountId int64)
	}
)

func (this* PlayerMgr) Init(num int){
	this.m_db = world.SERVER.GetDB()
	this.m_Log = world.SERVER.GetLog()
	this.m_PlayerMap = make(map[int64] *Player)
	this.m_Lock = &sync.RWMutex{}
	this.Actor.Init(num)
	actor.MGR().AddActor(this)
	//玩家登录
	this.RegisterCall("G_W_CLoginRequest", func(accountId int64) {
		pPlayer := this.GetPlayer(accountId)
		if pPlayer != nil{
			pPlayer.SendMsg("Logout", accountId)
			this.RemovePlayer(accountId)
		}

		pPlayer = this.AddPlayer(accountId)
		pPlayer.SendMsg("Login", this.GetSocketId())
	})

	//玩家断开链接
	this.RegisterCall("G_ClientLost", func(accountId int64) {
		pPlayer := this.GetPlayer(accountId)
		if pPlayer != nil{
			pPlayer.SendMsg("Logout", accountId)
		}

		this.RemovePlayer(accountId)
	})

	//account创建玩家反馈， 考虑到在创建角色的时候退出的情况
	this.RegisterCall("A_W_CreatePlayer", func(accountId int64, playerId int64, playername string, sex int32, socketId int) {
		rows, err := this.m_db.Query(fmt.Sprintf("call `sp_createplayer`(%d,'%s',%d, %d)", accountId, playername, sex, playerId))
		if err == nil && rows != nil{
			if rows.NextResultSet() && rows.NextResultSet(){
				rs := db.Query(rows)
				if rs.Next(){
					err := rs.Row().Int("@err")
					playerId := rs.Row().Int64("@playerId")
					//register
					if (err == 0) {
						this.m_Log.Printf("账号[%d]创建玩家[%d]", accountId, playerId)
					} else {
						this.m_Log.Printf("账号[%d]创建玩家失败", accountId)
						world.SERVER.GetAccountSocket().SendMsg("W_A_DeletePlayer", accountId, playerId)
					}

					//通知玩家`
					pPlayer := this.GetPlayer(accountId)
					if pPlayer != nil {
						pPlayer.SendMsg("CreatePlayer", playerId, socketId, err)
					}
				}
			}
		}
	})

	//this.RegisterTimer(1000 * 1000 * 1000, this.Update)//定时器
	PLAYER.Init(1)
	this.Actor.Start()
}

func (this *PlayerMgr) GetPlayer(accountId int64) *Player{
	this.m_Lock.RLock()
	pPlayer, exist := this.m_PlayerMap[accountId]
	this.m_Lock.RUnlock()
	if exist{
		return pPlayer
	}
	return nil
}

func (this *PlayerMgr) AddPlayer(accountId int64) *Player{
	LoadPlayerDB := func(accountId int64) ([]int64, int){
		PlayerList := make([]int64, 0)
		PlayerNum := 0
		rows, err := this.m_db.Query(fmt.Sprintf("select player_id from tbl_player where account_id=%d", accountId))
		rs := db.Query(rows)
		if err == nil{
			for rs.Next(){
				PlayerId := rs.Row().Int64("player_id")
				PlayerList = append(PlayerList, PlayerId)
				PlayerNum++
			}
		}
		return PlayerList, PlayerNum
	}

	fmt.Printf("玩家[%d]登录", accountId)
	PlayerList, PlayerNum := LoadPlayerDB(accountId)
	pPlayer := &Player{}
	pPlayer.AccountId = accountId
	pPlayer.PlayerIdList = PlayerList
	pPlayer.PlayerNum = PlayerNum
	this.m_Lock.Lock()
	this.m_PlayerMap[accountId] = pPlayer
	this.m_Lock.Unlock()
	pPlayer.Init(MAX_PLAYER_CHAN)
	return pPlayer
}

func (this *PlayerMgr) RemovePlayer(accountId int64){
	this.m_Log.Printf("移除帐号数据[%d]", accountId)
	this.m_Lock.Lock()
	delete(this.m_PlayerMap, accountId)
	this.m_Lock.Unlock()
}

func (this *PlayerMgr) PacketFunc(id int, buff []byte) bool{
	defer func() {
		if err := recover(); err != nil {
			fmt.Println("PlayerMgr PacketFunc", err)
		}
	}()

	SendToPlayer := func(AccountId int64, io actor.CallIO) {
		pPlayer := this.GetPlayer(AccountId)
		if pPlayer != nil{
			pPlayer.Send(io)
		}
	}

	var io actor.CallIO
	io.Buff = buff
	io.SocketId = id

	bitstream := base.NewBitStream(io.Buff, len(io.Buff))
	funcName := bitstream.ReadString()
	funcName = strings.ToLower(funcName)
	pFunc := this.FindCall(funcName)
	if pFunc != nil{
		this.Send(io)
		return true
	}else{
		pFunc := PLAYER.FindCall(funcName)
		if pFunc != nil{
			bitstream.ReadInt(base.Bit8)
			nType := bitstream.ReadInt(base.Bit8)
			if (nType == base.RPC_Int64 || nType == base.RPC_UInt64 || nType == base.RPC_PInt64 || nType == base.RPC_PUInt64){
				nAccountId := bitstream.ReadInt64(base.Bit64)
				SendToPlayer(nAccountId, io)
			}else if (nType == base.RPC_Message){
				packet := message.GetPakcetByName(funcName)
				message.UnmarshalText(packet, bitstream.ReadString())
				packetHead := message.GetPakcetHead(packet)
				nAccountId := int64(*packetHead.Id)
				SendToPlayer(nAccountId, io)
			}
		}
	}

	return false
}

//--------------发送给客户端----------------------//
func SendToClient(AccountId int64, packet proto.Message){
	pPlayer := PLAYERMGR.GetPlayer(AccountId)
	if pPlayer != nil{
		 world.SendToClient(pPlayer.SocketId, packet)
	}
}