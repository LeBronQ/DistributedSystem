package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"strconv"
	"context"

	"github.com/LeBronQ/Mobility"
	"github.com/LeBronQ/RadioChannelModel"
	"github.com/LeBronQ/tasks"
	"github.com/hibiken/asynq"
	"github.com/go-redis/redis/v8"
	"github.com/spf13/viper"
	
	consulapi "github.com/hashicorp/consul/api"
)

const (
	consul_address = "127.0.0.1:8500"
	redisAddr      = "127.0.0.1:6379"
)

var (
	NodeNum        = 100
)

var mobility_se = Discovery("Default_MobilityModel")

type Node struct {
	ID      int64
	MobNode Mobility.Node
	WNode   RadioChannelModel.WirelessNode
	Range   float64
}

type ChannelModel struct {
	LargeScaleModel string `json:"largescalemodel"`
	SmallScaleModel string `json:"smallscalemodel"`
}

type Position struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	Z float64 `json:"z"`
}

type WirelessNode struct {
	Frequency  float64 `json:"frequency"`
	BitRate    float64 `json:"bitrate"`
	Modulation string  `json:"modulation"`
	BandWidth  float64 `json:"bandwidth"`
	M          float64 `json:"m"`
	PowerInDbm float64 `json:"powerindbm"`
}

type ChannelReqParams struct {
	LinkId     int64                          `json:"linkid"`
	TxNode     RadioChannelModel.WirelessNode `json:"txnode"`
	RxNode     RadioChannelModel.WirelessNode `json:"rxnode"`
	TxPosition RadioChannelModel.Position     `json:"txposition"`
	RxPosition RadioChannelModel.Position     `json:"rxposition"`
	Model      ChannelModel                   `json:"model"`
}

type MobilityReqParams struct {
	Node Mobility.Node `json:"node"`
}

type TreeNodeData struct {
	ID int64
}

type DeliveryPoint struct {
	Coordinates []float64 `json:"coordinates"`
	ID          int64     `json:"id"`
}

type KDtreeDeliveryPayload struct {
	TreeNodes []DeliveryPoint
}

func GenerateNodes() []*Node {
	arr := make([]*Node, NodeNum)
	for i := 0; i < NodeNum; i++ {
		node := &Mobility.Node{
			Pos:  Mobility.Nbox.RandomPosition3D(),
			Time: 10,
			V: Mobility.Speed{
				X: 10., Y: 10., Z: 10.,
			},
			Model: "RandomWalk",
			Param: Mobility.RandomWalkParam{
				MinSpeed: 0,
				MaxSpeed: 20,
			},
		}
		wirelessNode := &RadioChannelModel.WirelessNode{
			Frequency:  2.4e+9,
			BitRate:    5.0e+7,
			Modulation: "BPSK",
			BandWidth:  2.0e+7,
			M:          0,
			PowerInDbm: 20,
		}
		n := &Node{
			ID:      int64(i),
			MobNode: *node,
			WNode:   *wirelessNode,
			Range:   2000.0,
		}
		arr[i] = n
	}
	return arr
}

func Discovery(serviceName string) []*consulapi.ServiceEntry {
	config := consulapi.DefaultConfig()
	config.Address = consul_address
	client, err := consulapi.NewClient(config)
	if err != nil {
		fmt.Printf("consul client error: %v", err)
	}
	service, _, err := client.Health().Service(serviceName, "", false, nil)
	if err != nil {
		fmt.Printf("consul client get serviceIp error: %v", err)
	}
	return service
}

func MobilityRequest(node Mobility.Node, se *consulapi.ServiceEntry) []byte {
	//se := Discovery("Default_MobilityModel")
	port := se.Service.Port
	address := se.Service.Address
	request := "http://" + address + ":" + strconv.Itoa(port) + "/mobility"
	param := MobilityReqParams{
		Node: node,
	}
	jsonData, err := json.Marshal(param)
	if err != nil {
		fmt.Println("Error encoding JSON:", err)
		return nil
	}

	requestBody := bytes.NewBuffer(jsonData)

	req, err := http.NewRequest("POST", request, requestBody)
	if err != nil {
		fmt.Println(err)
		return nil
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Length", fmt.Sprintf("%d", requestBody.Len()))

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Println(err)
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fmt.Println("Unexpected status code:", resp.StatusCode)
		return nil
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		fmt.Println("Error reading response body:", err)
		return nil
	}
	return body
}

func UpdatePosition(NodeArr []*Node) {
	for _, node := range NodeArr {
		res := MobilityRequest(node.MobNode, mobility_se[0])
		var newNode MobilityReqParams
		err := json.Unmarshal(res, &newNode)
		if err != nil {
			fmt.Println("Error:", err)
		}
		node.MobNode = newNode.Node
	}
}

var NodeArr []*Node

func main() {
	viper.SetConfigFile("../config.yaml")
	err := viper.ReadInConfig()
	if err != nil {
		fmt.Println("读取配置文件失败:", err)
		return
	}
	NodeNum = viper.GetInt("NodeNum")
	WorkerNum := viper.GetInt("WorkerNum")
	
	NodeArr = GenerateNodes()
	client := asynq.NewClient(asynq.RedisClientOpt{Addr: redisAddr})
	
	redisClient := redis.NewClient(&redis.Options{
		Addr: redisAddr, 
	})
	pubsub := redisClient.Subscribe(context.Background(), "task_notification")
	defer pubsub.Close()
	
	defer client.Close()

	deli_nodes := []tasks.DeliveryPoint{}
	UpdatePosition(NodeArr)

	for _, n := range NodeArr {
		deli_n := tasks.DeliveryPoint{Coordinates: []float64{n.MobNode.Pos.X, n.MobNode.Pos.Y, n.MobNode.Pos.Z}, ID: n.ID}
		deli_nodes = append(deli_nodes, deli_n)
	}

	task, err := tasks.NewKDtreeDeliveryTask(deli_nodes)
	if err != nil {
		log.Fatalf("could not create task: %v", err)
	}
	
	for i := 1; i <= WorkerNum; i++ {
		queue_name := fmt.Sprintf("queue%d", i)
		_, err = client.Enqueue(task, asynq.Queue(queue_name))
		if err != nil {
			log.Fatalf("could not enqueue task: %v", err)
		}
	}
	//log.Printf("enqueued task: id=%s queue=%s", info.ID, info.Queue)
	
	worker_num := 2
	
	msg := pubsub.Channel()
	finish_cnt := 0
	for _ = range msg {
		finish_cnt++
		if finish_cnt == worker_num {
			break
		}
	}
	
}
