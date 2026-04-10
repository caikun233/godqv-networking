package main

import (
	"fmt"
	"log"
	"runtime"
	"strings"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
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

func main() {
	// On Windows, attempt to self-elevate via UAC if not already running as
	// administrator. This is important because creating TUN devices (wintun)
	// requires admin privileges.
	ensureElevated()

	a := app.New()
	w := a.NewWindow("神区互联 - GodQV Networking")
	w.Resize(fyne.NewSize(520, 600))

	gui := &GUI{
		app:    a,
		window: w,
	}
	gui.showLoginScreen()

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
			cfg := client.Config{
				ServerAddr: server,
				Username:   user,
				Password:   pass,
			}
			c := client.New(cfg)
			if err := c.Connect(); err != nil {
				fyne.Do(func() {
					statusLabel.SetText(fmt.Sprintf("连接失败: %v", err))
				})
				return
			}

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
					g.client.SetConfig(client.Config{
						ServerAddr: g.client.ServerAddr(),
						Username:   g.client.Username(),
						RoomName:   roomName,
						RoomPass:   pass,
					})
					if err := g.client.JoinRoom(); err != nil {
						fyne.Do(func() {
							statusLabel.SetText(fmt.Sprintf("加入失败: %v", err))
						})
						return
					}
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
	vipLabel := widget.NewLabel(fmt.Sprintf("虚拟IP: %s", g.client.VirtualIP()))
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
		case p2p.EventPunchSuccess:
			msg = fmt.Sprintf("P2P: 与 %s 打洞成功! (UDP: %s)", event.PeerVIP, event.PeerAddr)
		case p2p.EventPunchTimeout:
			msg = fmt.Sprintf("P2P: 与 %s 打洞超时 (对方可能在对称NAT后, 将使用TCP中继)", event.PeerVIP)
		}
		if msg != "" {
			fyne.Do(func() {
				p2pStatusLabel.SetText(msg)
			})
		}
	})

	// Try to initialise P2P (after setting event callback)
	var p2pInitErr string
	if err := g.client.InitP2P(); err != nil {
		p2pInitErr = fmt.Sprintf("P2P初始化失败: %v (将使用TCP中继)", err)
		log.Printf("%s", p2pInitErr)
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
			return widget.NewLabel("peer")
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
				obj.(*widget.Label).SetText(fmt.Sprintf("%s - %s [%s] (%s)", p.Username, p.VirtualIP, status, mode))
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
		vipLabel,
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

	bottom := container.NewVBox(
		widget.NewSeparator(),
		disconnectBtn,
	)

	content := container.NewBorder(info, bottom, nil, nil, peerList)
	g.window.SetContent(container.NewPadded(content))

	// Monitor connection
	go func() {
		<-g.client.Done()
		fyne.Do(func() {
			statusLabel.SetText("连接已断开")
		})
	}()
}
