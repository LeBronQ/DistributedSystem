package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"strconv"

	"github.com/LeBronQ/Mobility"
	"github.com/LeBronQ/RadioChannelModel"
	"github.com/LeBronQ/kdtree"
	"github.com/LeBronQ/kdtree/points"
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
	StartIndex     = 0
	EndIndex       = NodeNum / 2
)

var channel_se = Discovery("Default_ChannelModel")

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

func ChannelRequest(Tx RadioChannelModel.WirelessNode, Rx RadioChannelModel.WirelessNode, TxPos RadioChannelModel.Position, RxPos RadioChannelModel.Position, se *consulapi.ServiceEntry) {
	//se := Discovery("Default_ChannelModel")
	port := se.Service.Port
	address := se.Service.Address
	request := "http://" + address + ":" + strconv.Itoa(port) + "/model"
	mod := ChannelModel{
		LargeScaleModel: "FreeSpacePathLossModel",
		SmallScaleModel: "NakagamiFadingModel",
	}
	param := ChannelReqParams{
		LinkId:     0,
		TxNode:     Tx,
		RxNode:     Rx,
		TxPosition: TxPos,
		RxPosition: RxPos,
		Model:      mod,
	}

	jsonData, err := json.Marshal(param)
	if err != nil {
		fmt.Println("Error encoding JSON:", err)
		return
	}

	requestBody := bytes.NewBuffer(jsonData)

	req, err := http.NewRequest("POST", request, requestBody)
	if err != nil {
		fmt.Println(err)
		return
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Length", fmt.Sprintf("%d", requestBody.Len()))

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Println(err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fmt.Println("Unexpected status code:", resp.StatusCode)
		return
	}

	_, err = ioutil.ReadAll(resp.Body)
	if err != nil {
		fmt.Println("Error reading response body:", err)
		return
	}

	//fmt.Println("Response:", string(body))
}

func UpdateNeighborsAndCalculatePLR(tree *kdtree.KDTree, ctx context.Context) {
	cnt := 0
	for i := StartIndex; i < EndIndex; i++ {
		cnt++
		node := NodeArr[i]
		distance := node.Range
		center := points.NewPoint([]float64{node.MobNode.Pos.X, node.MobNode.Pos.Y, node.MobNode.Pos.Z}, TreeNodeData{ID: node.ID})
		res := tree.QueryBallPoint(center, distance)
		//fmt.Printf("%v\n",res[0].GetData().(TreeNodeData).ID)
		for _, neigh := range res {
			neigh_ID := neigh.GetData().(TreeNodeData).ID
			if node.ID == neigh_ID {
				continue
			} else {
				neigh_node := NodeArr[neigh_ID]
				ChannelRequest(node.WNode, neigh_node.WNode, RadioChannelModel.Position(node.MobNode.Pos), RadioChannelModel.Position(neigh_node.MobNode.Pos), channel_se[0])
			}
		}
		//fmt.Printf("%v\n", graph[i])
	}
	fmt.Println("cnt:", cnt)
	redisClient := redis.NewClient(&redis.Options{
		Addr: redisAddr, 
	})
	TaskFinishInform(redisClient, ctx)
}

func TaskFinishInform(redisClient *redis.Client, ctx context.Context) error {
	err := redisClient.Publish(ctx, "task_notification", fmt.Sprintf("Task 1 completed")).Err()
	if err != nil {
		return err
	}
	return nil
}

func HandleKDtreeDeliveryTask(ctx context.Context, t *asynq.Task) error {
	var payload KDtreeDeliveryPayload
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		return fmt.Errorf("json.Unmarshal failed: %v: %w", err, asynq.SkipRetry)
	}
	var nodes []kdtree.Point
	for _, n := range payload.TreeNodes {
		NodeArr[n.ID].MobNode.Pos.X = n.Coordinates[0]
		NodeArr[n.ID].MobNode.Pos.Y = n.Coordinates[1]
		NodeArr[n.ID].MobNode.Pos.Z = n.Coordinates[2]
		p := points.NewPoint(n.Coordinates, TreeNodeData{ID: n.ID})
		nodes = append(nodes, p)
	}
	tree := kdtree.New(nodes)
	UpdateNeighborsAndCalculatePLR(tree, ctx)
	
	return nil
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
	EndIndex = NodeNum / WorkerNum
	
	NodeArr = GenerateNodes()
	srv := asynq.NewServer(
		asynq.RedisClientOpt{Addr: redisAddr},
		asynq.Config{
			// Specify how many concurrent workers to use
			Concurrency: 1,
			// Optionally specify multiple queues with different priority.
			Queues: map[string]int{
                		"queue1": 6,
            		},
			// See the godoc for other configuration options
		},
	)
	// mux maps a type to a handler
	mux := asynq.NewServeMux()
	mux.HandleFunc(tasks.TypeKDtreeDelivery, HandleKDtreeDeliveryTask)

	if err := srv.Run(mux); err != nil {
		log.Fatalf("could not run server: %v", err)
	}
}
