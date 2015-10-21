package main

import (
    "errors"
    "flag"
    "log"
    "os"
    "io/ioutil"
    "os/exec"
    "syscall"
    "time"
    "encoding/json"
    "strings"
    "path/filepath"

    "gopkg.in/fsnotify.v1"
    "github.com/mitchellh/go-ps"
)

const (
    BINFILE = "bin.file"
    CFGFILE = "cfg.file"
)
var (
    logger *log.Logger
    restartChan chan bool
    started = false
    binFile string
)

var config = map[string]string {
    "notify.file": "./notify.file",  // The directory to watch for the run target
    "log.file": "./gorun.log",       // gorun output log
}

func main() {
    configFile := flag.String("c", "gorun.conf", "Gorun config file path.")
    flag.Parse()

    if err := loadConfig(*configFile); err != nil {
        log.Fatalf("Load config file error: %v", err)
    }

    f, err := os.OpenFile(config["log.file"], os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
	if err != nil {
		log.Fatalf("Open log file error: %v", err)
	}

    logger = log.New(f, "[gorun] ", log.Ldate | log.Ltime)

    restartChan = make(chan bool)
    go watch()
    go run()
    runWatchDog()

    <- make(chan bool)
}

func loadConfig(file string) error {
	c, err := ioutil.ReadFile(file)
	if err != nil {
		return err
	}

	var j interface{}
	err = json.Unmarshal(c, &j)
	if err != nil {
		return err
	}

	cfg, ok := j.(map[string]interface{})
	if !ok {
		return errors.New("error on mapping config")
	}

    for k, _ := range config {
        if cfg[k] != nil {
            config[k] = cfg[k].(string)
        }
    }
    return nil
}

func watch() {
    watcher, err := fsnotify.NewWatcher()
    if err != nil {
        logger.Fatal(err)
    }
    defer watcher.Close()

    go func() {
        for {
                select {
                case event := <-watcher.Events:
                    logger.Println("event:", event)
                    if event.Op&fsnotify.Write == fsnotify.Write {
                        logger.Println("notify runner to do the ln -s and restart server.")
                        restartChan <- true
                    }
                case err := <-watcher.Errors:
                    logger.Println("error:", err)
            }
        }
    }()

    err = watcher.Add(config["notify.file"])
    if err != nil {
        logger.Fatal(err)
    }

    <- make(chan bool)
}

func run() {
    for {
        <- restartChan

        c, err := ioutil.ReadFile(config["notify.file"])
        if err != nil {
            logger.Println("notify file read error:", err)
            return
        }

        var j interface{}
        err = json.Unmarshal(c, &j)
        if err != nil {
            logger.Println("notify file parse error:", err)
            return
        }

        parsed, ok := j.(map[string]interface{})
        if !ok {
            logger.Println("notify file parse error: mapping errors")
            return
        }

        if err = exec.Command("rm", BINFILE).Run(); err != nil {
            logger.Println("RM file error:", BINFILE)
        }

        if err = exec.Command("ln", "-s", parsed[BINFILE].(string), BINFILE).Run(); err != nil {
            logger.Println("LN -s file error:", parsed[BINFILE].(string))
            return
        }

        if err = exec.Command("rm", CFGFILE).Run(); err != nil {
            logger.Println("RM file error:", CFGFILE)
        }

        if err = exec.Command("ln", "-s", parsed[CFGFILE].(string), CFGFILE).Run(); err != nil {
            logger.Println("LN -s file error:", parsed[CFGFILE].(string))
            return
        }

        if !started {
            logger.Println("command starting")
            cmd := exec.Command("./" + BINFILE, "-c", CFGFILE)
            err = cmd.Start()
            if err != nil {
                logger.Println("command error: ", err)
            }
            started = true
        } else {
            logger.Println("Send SIGHUP signal")
            processes, _ := ps.Processes()
            for _, v := range processes {
                if strings.Contains(v.Executable(), binFile) {
                    logger.Println("Found the bin file pid")
                    process, err := os.FindProcess(v.Pid())
                    if err != nil {
                        logger.Println("Error: get process from pid")
                    }
                    logger.Println("Signal bin process")
                    process.Signal(syscall.SIGHUP)
                }
            }
        }
        binFile = filepath.Base(parsed[BINFILE].(string))
    }
}

func runWatchDog() {
	timer := time.NewTicker(5 * time.Second)
	go func() {
		for {
			select {
			case <-timer.C:
				if started {
                    running := false
                    processes, _ := ps.Processes()
                    for _, v := range processes {
                        if strings.Contains(v.Executable(), binFile) {
                            running = true
                            logger.Println("pid running: ", v.Executable(), "Pid:", v.Pid(), "BinFile:", binFile)
                            break
                        }
                    }

                    if !running {
                        logger.Println("Command is terminal, restarting...")
                        cmd := exec.Command("./" + BINFILE, "-c", CFGFILE)
                        err := cmd.Start()
                        if err != nil {
                            logger.Println("command error: ", err)
                        }
                    }
                }
			}
		}
	}()
}
