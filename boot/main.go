package main

import (
	"fmt"
	"os/exec"
	"os"
	"path/filepath"
	"syscall"
	
	"github.com/spf13/viper"
)

func terminateProcess(pid int) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}

	// 使用系统调用发送终止信号
	err = process.Signal(syscall.SIGTERM)
	if err != nil {
		return err
	}

	return nil
}

func main() {
	viper.SetConfigFile("../config.yaml")
	err := viper.ReadInConfig()
	if err != nil {
		fmt.Println("读取配置文件失败:", err)
		return
	}
	 
	WorkerNum := viper.GetInt("WorkerNum")
	PID_arr := []int{}
		
	for i := 1; i <= WorkerNum; i++ {
		currentDir, err := os.Getwd()
		if err != nil {
			fmt.Println("获取当前path失败:", err)
			return
		}

		parentDir := filepath.Dir(currentDir)
		workerDir := filepath.Join(parentDir, fmt.Sprintf("worker%d", i))
		fmt.Println(workerDir)
		
		err = os.Chdir(workerDir)
		if err != nil {
			fmt.Println("进入 worker目录失败:", err)
			return
		}
		
		cmd := exec.Command("sudo", "go", "run", "main.go")
		err = cmd.Start()
		if err != nil {
			fmt.Println("执行命令失败:", err)
			return
		}
		PID_arr = append(PID_arr, cmd.Process.Pid)
	}
	
	
	select{}
}
