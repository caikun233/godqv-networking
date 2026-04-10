package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/data/binding"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/caikun233/godqv-networking/internal/client"
	"github.com/caikun233/godqv-networking/pkg/p2p"
	"github.com/caikun233/godqv-networking/pkg/tunnel"
)

const (
	prefKeyServer   = "login_server"
	prefKeyUsername = "login_username"
	prefKeyPassword = "login_password"
)

// tunWrapper wraps a tunnel.Device for use as client.TunWriter.
type tunWrapper struct {
	dev tunnel.Device
}

func (tw *tunWrapper) WritePacket(packet []byte) error {
	_, err := tw.dev.Write(packet)
	return err
}

type memLogger struct {
	mu      sync.Mutex
	lines   []string
	logData binding.StringList // nil until InitBinding() is called
}

func (l *memLogger) Write(p []byte) (n int, err error) {
	text := strings.TrimRight(string(p), "\n")
	if text == "" {
		return len(p), nil
	}
	l.mu.Lock()
	l.lines = append(l.lines, text)
	if len(l.lines) > 1000 {
		l.lines = l.lines[len(l.lines)-1000:]
	}
	logData := l.logData
	var snapshot []string
	if logData != nil {
		snapshot = make([]string, len(l.lines))
		copy(snapshot, l.lines)
	}
	l.mu.Unlock()

	if logData != nil {
		// Update binding – Fyne binding is thread-safe and auto-notifies the list.
		_ = logData.Set(snapshot)
	}
	return len(p), nil
}

// InitBinding creates the Fyne data binding and syncs all accumulated log
// lines into it.  Must be called after the Fyne app has been created so that
// the binding infrastructure is ready.
func (l *memLogger) InitBinding() {
	l.mu.Lock()
	l.logData = binding.NewStringList()
	snapshot := make([]string, len(l.lines))
	copy(snapshot, l.lines)
	l.mu.Unlock()
	_ = l.logData.Set(snapshot)
}

func (l *memLogger) Lines() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	result := make([]string, len(l.lines))
	copy(result, l.lines)
	return result
}

var globalLogger = &memLogger{}

// openLogFile creates (or opens for append) the log file in the user config directory.
// Returns the file, its path, and any error.
func openLogFile() (*os.File, string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		configDir = os.TempDir()
	}
	logDir := filepath.Join(configDir, "GodQV Networking")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return nil, "", fmt.Errorf("无法创建日志目录: %w", err)
	}
	logPath := filepath.Join(logDir, "godqv.log")
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, "", err
	}
	return f, logPath, nil
}

func main() {
	// On Windows, allocate a console window so that all log output is visible
	// to the user for diagnostics (e.g. UDP hole-punching debugging).
	attachConsole()

	// Setup logger: write to the log file and the in-memory logger (which
	// feeds the in-app log viewer once the Fyne app is initialised).
	// Note: globalLogger buffers lines in a plain slice until InitBinding()
	// is called after app.New(), avoiding Fyne API calls before the driver
	// is ready.
	writers := []io.Writer{os.Stdout, globalLogger}
	logFile, logFilePath, err := openLogFile()
	if err == nil {
		writers = append(writers, logFile)
		defer logFile.Close()
	}
	log.SetOutput(io.MultiWriter(writers...))
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	if err != nil {
		log.Printf("警告: 无法打开日志文件: %v", err)
	} else {
		log.Printf("日志文件: %s", logFilePath)
	}

	log.Printf("[启动] 操作系统: %s/%s, Go版本: %s, PID: %d",
		runtime.GOOS, runtime.GOARCH, runtime.Version(), os.Getpid())

	// On Windows, attempt to self-elevate via UAC if not already running as
	// administrator. This is important because creating TUN devices (wintun)
	// requires admin privileges.
	log.Printf("[启动] 检查管理员权限...")
	ensureElevated()
	log.Printf("[启动] 权限检查完成")

	log.Printf("[启动] 正在初始化 Fyne GUI 框架...")
	a := app.New()
	globalLogger.InitBinding()
	log.Printf("[启动] Fyne app 创建成功")
	a.SetIcon(AppIcon)
	w := a.NewWindow("神区互联 - GodQV Networking")
	w.SetIcon(AppIcon)
	w.Resize(fyne.NewSize(520, 600))
	log.Printf("[启动] 主窗口创建成功 (520x600)")

	gui := &GUI{
		app:    a,
		window: w,
	}
	gui.showLoginScreen()
	log.Printf("[启动] 登录界面已加载, 即将显示窗口...")

	w.ShowAndRun()
}

// GUI holds the application state.
type GUI struct {
	app    fyne.App
	window fyne.Window
	client *client.Client
	tunDev tunnel.Device
	mu     sync.Mutex
}

func (g *GUI) showLoginScreen() {
	// Form fields - restore saved values from preferences
	prefs := g.app.Preferences()

	serverEntry := widget.NewEntry()
	serverEntry.SetPlaceHolder("服务器地址 (例如: example.com:9527)")
	serverEntry.SetText(prefs.StringWithFallback(prefKeyServer, ""))

	userEntry := widget.NewEntry()
	userEntry.SetPlaceHolder("用户名")
	userEntry.SetText(prefs.StringWithFallback(prefKeyUsername, ""))

	passEntry := widget.NewPasswordEntry()
	passEntry.SetPlaceHolder("密码 (留空表示无密码登录)")
	passEntry.SetText(prefs.StringWithFallback(prefKeyPassword, ""))

	statusLabel := widget.NewLabel("")
	statusLabel.Wrapping = fyne.TextWrapWord

	// Login button
	loginBtn := widget.NewButtonWithIcon("登录", theme.LoginIcon(), func() {
		server := strings.TrimSpace(serverEntry.Text)
		user := strings.TrimSpace(userEntry.Text)
		pass := passEntry.Text

		if server == "" || user == "" {
			statusLabel.SetText("请输入服务器地址和用户名")
			return
		}

		// Save login info to preferences
		prefs.SetString(prefKeyServer, server)
		prefs.SetString(prefKeyUsername, user)
		prefs.SetString(prefKeyPassword, pass)

		statusLabel.SetText("正在连接...")

		go func() {
			log.Printf("[GUI] 用户发起连接: 服务器=%s, 用户名=%s", server, user)
			cfg := client.Config{
				ServerAddr: server,
				Username:   user,
				Password:   pass,
			}
			c := client.New(cfg)
			if err := c.Connect(); err != nil {
				log.Printf("[GUI] 连接失败: %v", err)
				fyne.Do(func() {
					statusLabel.SetText(fmt.Sprintf("连接失败: %v", err))
				})
				return
			}
			log.Printf("[GUI] 连接成功, 进入房间选择界面")

			g.mu.Lock()
			g.client = c
			g.mu.Unlock()

			fyne.Do(func() {
				g.showRoomScreen()
			})
		}()
	})

	// Register button
	registerBtn := widget.NewButton("注册新用户", func() {
		server := strings.TrimSpace(serverEntry.Text)
		user := strings.TrimSpace(userEntry.Text)
		pass := passEntry.Text

		if server == "" || user == "" {
			statusLabel.SetText("请输入服务器地址和用户名")
			return
		}

		statusLabel.SetText("正在注册...")

		go func() {
			c := client.New(client.Config{})
			if err := c.Register(server, user, pass); err != nil {
				fyne.Do(func() {
					statusLabel.SetText(fmt.Sprintf("注册失败: %v", err))
				})
				return
			}
			fyne.Do(func() {
				statusLabel.SetText("注册成功！请登录")
			})
		}()
	})

	// Layout
	title := widget.NewRichTextFromMarkdown("# 神区互联\n### GodQV Networking")
	title.Wrapping = fyne.TextWrapWord

	form := container.NewVBox(
		title,
		widget.NewSeparator(),
		widget.NewLabel("服务器地址:"),
		serverEntry,
		widget.NewLabel("用户名:"),
		userEntry,
		widget.NewLabel("密码:"),
		passEntry,
		layout.NewSpacer(),
		container.NewGridWithColumns(2, loginBtn, registerBtn),
		statusLabel,
	)

	g.window.SetContent(container.NewPadded(form))
}

func (g *GUI) showRoomScreen() {
	statusLabel := widget.NewLabel("已连接，请双击房间加入")
	statusLabel.Wrapping = fyne.TextWrapWord

	// Room list
	roomList := widget.NewList(
		func() int { return 0 },
		func() fyne.CanvasObject {
			return widget.NewLabel("room")
		},
		func(id widget.ListItemID, obj fyne.CanvasObject) {},
	)

	var rooms []string
	var roomsMu sync.Mutex

	refreshRooms := func() {
		roomInfos, err := g.client.ListRooms()
		if err != nil {
			fyne.Do(func() {
				statusLabel.SetText(fmt.Sprintf("获取房间列表失败: %v", err))
			})
			return
		}
		roomsMu.Lock()
		rooms = make([]string, len(roomInfos))
		for i, r := range roomInfos {
			rooms[i] = r.Name
		}
		roomsMu.Unlock()
		fyne.Do(func() {
			roomList.Length = func() int {
				roomsMu.Lock()
				defer roomsMu.Unlock()
				return len(rooms)
			}
			roomList.UpdateItem = func(id widget.ListItemID, obj fyne.CanvasObject) {
				roomsMu.Lock()
				defer roomsMu.Unlock()
				if id < len(rooms) {
					obj.(*widget.Label).SetText(rooms[id])
				}
			}
			roomList.Refresh()
		})
	}

	// Double-click room to join: show password dialog
	joinRoom := func(roomName string) {
		passEntry := widget.NewPasswordEntry()
		passEntry.SetPlaceHolder("房间密码")

		items := []*widget.FormItem{
			widget.NewFormItem("密码", passEntry),
		}

		d := dialog.NewForm(
			fmt.Sprintf("加入房间: %s", roomName),
			"加入", "取消", items,
			func(ok bool) {
				if !ok {
					return
				}
				pass := passEntry.Text
				statusLabel.SetText(fmt.Sprintf("正在加入房间 %s...", roomName))

				go func() {
					log.Printf("[GUI] 加入房间: %s", roomName)
					g.client.SetConfig(client.Config{
						ServerAddr: g.client.ServerAddr(),
						Username:   g.client.Username(),
						RoomName:   roomName,
						RoomPass:   pass,
					})
					if err := g.client.JoinRoom(); err != nil {
						log.Printf("[GUI] 加入房间失败: %v", err)
						fyne.Do(func() {
							statusLabel.SetText(fmt.Sprintf("加入失败: %v", err))
						})
						return
					}
					log.Printf("[GUI] 加入房间成功: %s", roomName)
					fyne.Do(func() {
						g.showMainScreen()
					})
				}()
			}, g.window)
		d.Resize(fyne.NewSize(400, 150))
		d.Show()
	}

	roomList.OnSelected = func(id widget.ListItemID) {
		roomsMu.Lock()
		var name string
		if id >= 0 && id < len(rooms) {
			name = rooms[id]
		}
		roomsMu.Unlock()
		roomList.UnselectAll()
		if name != "" {
			joinRoom(name)
		}
	}

	// Create room
	createBtn := widget.NewButton("创建新房间", func() {
		g.showCreateRoomDialog(statusLabel, refreshRooms)
	})

	// Refresh button
	refreshBtn := widget.NewButtonWithIcon("刷新", theme.ViewRefreshIcon(), func() {
		go refreshRooms()
	})

	// Layout
	top := container.NewVBox(
		widget.NewRichTextFromMarkdown("## 选择房间"),
		container.NewHBox(refreshBtn, createBtn),
	)

	bottom := container.NewVBox(
		statusLabel,
	)

	content := container.NewBorder(top, bottom, nil, nil, roomList)
	g.window.SetContent(container.NewPadded(content))

	// Initial load
	go refreshRooms()
}

func (g *GUI) showCreateRoomDialog(statusLabel *widget.Label, onCreated func()) {
	nameEntry := widget.NewEntry()
	nameEntry.SetPlaceHolder("房间名称")
	passEntry := widget.NewPasswordEntry()
	passEntry.SetPlaceHolder("房间密码 (必填)")

	items := []*widget.FormItem{
		widget.NewFormItem("房间名称", nameEntry),
		widget.NewFormItem("房间密码", passEntry),
	}

	d := dialog.NewForm("创建新房间", "创建", "取消", items, func(ok bool) {
		if !ok {
			return
		}
		name := strings.TrimSpace(nameEntry.Text)
		pass := passEntry.Text
		if name == "" || pass == "" {
			statusLabel.SetText("房间名称和密码不能为空")
			return
		}

		go func() {
			if err := g.client.CreateRoom(name, pass); err != nil {
				fyne.Do(func() {
					statusLabel.SetText(fmt.Sprintf("创建房间失败: %v", err))
				})
				return
			}
			fyne.Do(func() {
				statusLabel.SetText(fmt.Sprintf("房间 %s 创建成功", name))
			})
			onCreated()
		}()
	}, g.window)
	d.Resize(fyne.NewSize(400, 200))
	d.Show()
}

func (g *GUI) showMainScreen() {
	vipStr := g.client.VirtualIP().String()
	vipLabel := widget.NewLabel(fmt.Sprintf("虚拟IP: %s", vipStr))
	copyVipBtn := widget.NewButtonWithIcon("", theme.ContentCopyIcon(), func() {
		g.window.Clipboard().SetContent(vipStr)
	})

	subnet := g.client.Subnet()
	subnetLabel := widget.NewLabel(fmt.Sprintf("子网: %s", subnet.String()))

	statusLabel := widget.NewLabel("已连接")
	statusLabel.Wrapping = fyne.TextWrapWord

	p2pStatusLabel := widget.NewLabel("")
	p2pStatusLabel.Wrapping = fyne.TextWrapWord

	// Setup TUN device
	var tunStatus string
	if runtime.GOOS == "windows" {
		tunName := "GodQV Networking"
		tunDev, err := tunnel.CreateTUN(tunnel.Config{
			Name:    tunName,
			Address: g.client.VirtualIP(),
			Subnet:  g.client.Subnet(),
			MTU:     1400,
		})
		if err != nil {
			tunStatus = fmt.Sprintf("TUN: 创建失败 (需要管理员权限): %v", err)
			log.Printf("TUN创建失败: %v", err)
		} else {
			g.tunDev = tunDev
			tunStatus = fmt.Sprintf("TUN: %s", tunDev.Name())
			g.client.SetTunWriter(&tunWrapper{dev: tunDev})

			// Read from TUN and send to server
			go func() {
				buf := make([]byte, 1500)
				for {
					n, err := tunDev.Read(buf)
					if err != nil {
						return
					}
					if n > 0 {
						g.client.SendPacket(buf[:n])
					}
				}
			}()
		}
	} else {
		tunStatus = "TUN: 不支持的操作系统"
	}

	tunLabel := widget.NewLabel(tunStatus)

	// Set P2P event callback to report hole-punching status in GUI
	g.client.SetP2PEventCallback(func(event p2p.Event) {
		var msg string
		switch event.Type {
		case p2p.EventPunchStart:
			msg = fmt.Sprintf("P2P: 正在与 %s 打洞...", event.PeerVIP)
			log.Printf("[P2P-GUI] 开始打洞: 对端VIP=%s, 对端地址=%s", event.PeerVIP, event.PeerAddr)
		case p2p.EventPunchSuccess:
			msg = fmt.Sprintf("P2P: 与 %s 打洞成功! (UDP: %s)", event.PeerVIP, event.PeerAddr)
			log.Printf("[P2P-GUI] 打洞成功: 对端VIP=%s, UDP地址=%s", event.PeerVIP, event.PeerAddr)
		case p2p.EventPunchTimeout:
			msg = fmt.Sprintf("P2P: 与 %s 打洞超时 (对方可能在对称NAT后, 将使用TCP中继)", event.PeerVIP)
			log.Printf("[P2P-GUI] 打洞超时: 对端VIP=%s, 对端地址=%s (可能原因: 对称NAT/防火墙/Hairpin NAT不支持/对端离线)", event.PeerVIP, event.PeerAddr)
		}
		if msg != "" {
			fyne.Do(func() {
				p2pStatusLabel.SetText(msg)
			})
		}
	})

	// Try to initialise P2P (after setting event callback)
	var p2pInitErr string
	log.Printf("[GUI] 正在初始化 P2P...")
	if err := g.client.InitP2P(); err != nil {
		p2pInitErr = fmt.Sprintf("P2P初始化失败: %v (将使用TCP中继)", err)
		log.Printf("[GUI] %s", p2pInitErr)
	} else {
		log.Printf("[GUI] P2P 初始化成功")
	}

	// Peer list
	peerData := []client.PeerInfo{}
	var peerDataMu sync.Mutex

	peerList := widget.NewList(
		func() int {
			peerDataMu.Lock()
			defer peerDataMu.Unlock()
			return len(peerData)
		},
		func() fyne.CanvasObject {
			btn := widget.NewButtonWithIcon("", theme.ContentCopyIcon(), nil)
			lbl := widget.NewLabel("peer")
			return container.NewBorder(nil, nil, nil, btn, lbl)
		},
		func(id widget.ListItemID, obj fyne.CanvasObject) {
			peerDataMu.Lock()
			defer peerDataMu.Unlock()
			if id < len(peerData) {
				p := peerData[id]
				mode := "TCP中继"
				if p.P2P {
					mode = "P2P直连"
				}
				status := "在线"
				if !p.Online {
					status = "离线"
				}

				c := obj.(*fyne.Container)
				text := fmt.Sprintf("%s - %s [%s] (%s)", p.Username, p.VirtualIP, status, mode)
				for _, o := range c.Objects {
					switch v := o.(type) {
					case *widget.Label:
						v.SetText(text)
					case *widget.Button:
						v.OnTapped = func() {
							g.window.Clipboard().SetContent(p.VirtualIP.String())
						}
					}
				}
			}
		},
	)

	g.client.SetPeerUpdateCallback(func(peers []client.PeerInfo) {
		peerDataMu.Lock()
		peerData = peers
		peerDataMu.Unlock()
		fyne.Do(func() {
			peerList.Refresh()
		})
	})

	// Start receiving
	g.client.StartReceiving()

	// Disconnect button
	disconnectBtn := widget.NewButtonWithIcon("断开连接", theme.CancelIcon(), func() {
		g.client.Close()
		if g.tunDev != nil {
			g.tunDev.Close()
			g.tunDev = nil
		}
		g.showLoginScreen()
	})

	// Info panel
	infoItems := []fyne.CanvasObject{
		widget.NewRichTextFromMarkdown("## 神区互联 - 已连接"),
		widget.NewSeparator(),
		container.NewHBox(vipLabel, copyVipBtn),
		subnetLabel,
		tunLabel,
		statusLabel,
	}
	if p2pInitErr != "" {
		infoItems = append(infoItems, widget.NewLabel(p2pInitErr))
	}
	infoItems = append(infoItems, p2pStatusLabel)
	infoItems = append(infoItems,
		widget.NewSeparator(),
		widget.NewLabel("在线节点:"),
	)
	info := container.NewVBox(infoItems...)

	// 日志查看按钮 - 在新窗口中打开日志
	logBtn := widget.NewButtonWithIcon("运行日志", theme.DocumentIcon(), func() {
		g.showLogWindow()
	})

	bottom := container.NewVBox(
		widget.NewSeparator(),
		container.NewGridWithColumns(2, logBtn, disconnectBtn),
	)

	mainContent := container.NewBorder(info, bottom, nil, nil, peerList)

	g.window.SetContent(container.NewPadded(mainContent))

	// Monitor connection
	go func() {
		<-g.client.Done()
		fyne.Do(func() {
			statusLabel.SetText("连接已断开")
		})
	}()
}

// showLogWindow 打开一个单独的窗口显示运行日志。
func (g *GUI) showLogWindow() {
	logWindow := g.app.NewWindow("运行日志 - GodQV Networking")
	logWindow.Resize(fyne.NewSize(700, 500))

	// Use binding-based list so Fyne handles all refresh/threading automatically.
	logList := widget.NewListWithData(
		globalLogger.logData,
		func() fyne.CanvasObject {
			return widget.NewLabel("")
		},
		func(item binding.DataItem, obj fyne.CanvasObject) {
			str, _ := item.(binding.String).Get()
			obj.(*widget.Label).SetText(str)
		},
	)

	// Auto-scroll to the latest entry whenever new log lines arrive.
	scrollListener := binding.NewDataListener(func() {
		fyne.Do(func() {
			logList.ScrollToBottom()
		})
	})
	globalLogger.logData.AddListener(scrollListener)
	logWindow.SetOnClosed(func() {
		globalLogger.logData.RemoveListener(scrollListener)
	})

	// 清空日志按钮
	clearBtn := widget.NewButton("清空日志", func() {
		globalLogger.mu.Lock()
		globalLogger.lines = nil
		globalLogger.mu.Unlock()
		_ = globalLogger.logData.Set(nil)
	})

	// 复制全部日志按钮
	copyBtn := widget.NewButton("复制全部", func() {
		lines := globalLogger.Lines()
		g.window.Clipboard().SetContent(strings.Join(lines, "\n"))
	})

	toolbar := container.NewHBox(
		widget.NewLabel("日志条目: "),
		clearBtn,
		copyBtn,
	)

	content := container.NewBorder(toolbar, nil, nil, nil, logList)
	logWindow.SetContent(container.NewPadded(content))

	logWindow.Show()

	// Scroll to bottom after the window has been rendered.
	logList.ScrollToBottom()
}
