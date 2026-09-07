package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ================================================================
// Messages (background → UI)
// ================================================================

type bgMsg interface{ isBgMsg() }

type posMsg struct {
	lat, lon, height float64
	quality, nsat    int
	convergence      int
	isMoving         bool
}

func (posMsg) isBgMsg() {}

type candidateMsg struct {
	ip       string
	port     int
	isMoving bool
}

func (candidateMsg) isBgMsg() {}

type logMsg struct{ text string }

func (logMsg) isBgMsg() {}

type modeMsg struct{ mode string }

func (modeMsg) isBgMsg() {}

type ephMsg struct{ addr string }

func (ephMsg) isBgMsg() {}

type ssrMsg struct{ addr string }

func (ssrMsg) isBgMsg() {}

type stoppedMsg struct{}

func (stoppedMsg) isBgMsg() {}

type portMsg struct{ port int }

func (portMsg) isBgMsg() {}

type loginOKMsg struct{ token, login string }
type loginErrMsg struct{ err string }
type devicesMsg struct {
	devices []DeviceInfo
	err     string
}

// ================================================================
// Styles
// ================================================================

var (
	styleBorder = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("62"))

	styleTitle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("15")).
			Background(lipgloss.Color("62")).
			Padding(0, 1)

	styleLabel  = lipgloss.NewStyle().Foreground(lipgloss.Color("243"))
	styleValue  = lipgloss.NewStyle().Bold(true)
	styleOK     = lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true)
	styleWarn   = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	styleErr    = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	styleActive = lipgloss.NewStyle().Foreground(lipgloss.Color("212")).Bold(true)
)

// ================================================================
// Screens
// ================================================================

type screen int

const (
	screenLogin screen = iota
	screenMain
)

// ================================================================
// Model
// ================================================================

const (
	lURL  = 0
	lUser = 1
	lPass = 2
	lN    = 3
)

type Model struct {
	screen screen
	cfg    *Config
	api    *ServiceClient

	// Login
	loginInputs [lN]textinput.Model
	loginFocus  int
	loginStatus string
	loginIsErr  bool

	// Device (loaded from service after login)
	device *DeviceInfo

	// Position
	lat, lon, height float64
	quality          int
	nsat             int
	convergence      int
	isMoving         bool

	// Service
	solverMode   string
	ephAddr      string
	ssrAddr      string
	candidateTxt string
	outputPort   int
	running      bool

	// Log
	logs    []string
	maxLogs int

	// Background
	updates  chan tea.Msg
	stopChan chan struct{}

	// Terminal size
	termW, termH int
}

func newModel(cfg *Config) Model {
	m := Model{
		cfg:          cfg,
		screen:       screenLogin,
		maxLogs:      200,
		updates:      make(chan tea.Msg, 100),
		solverMode:   "—",
		candidateTxt: "None",
	}

	// Login inputs
	li := [lN]textinput.Model{}
	li[lURL] = newInput("http://localhost:8000", 50, false)
	li[lURL].SetValue(cfg.ServiceURL)
	li[lUser] = newInput("username", 30, false)
	li[lPass] = newInput("password", 30, true)
	if cfg.Token != "" {
		li[lUser].SetValue(cfg.UserLogin)
	}
	li[lURL].Focus()
	m.loginInputs = li

	return m
}

func newInput(placeholder string, w int, pwd bool) textinput.Model {
	ti := textinput.New()
	ti.Placeholder = placeholder
	ti.Width = w
	if pwd {
		ti.EchoMode = textinput.EchoPassword
	}
	return ti
}

// ================================================================
// Init
// ================================================================

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		textinput.Blink,
		m.waitForBg(),
	)
}

func (m Model) waitForBg() tea.Cmd {
	return func() tea.Msg { return <-m.updates }
}

// ================================================================
// Update
// ================================================================

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m.screen {
	case screenLogin:
		return m.updateLogin(msg)
	case screenMain:
		return m.updateMain(msg)
	}
	return m, nil
}

// ---- Login ----

func (m Model) updateLogin(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.termW, m.termH = msg.Width, msg.Height

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "tab", "down":
			m.loginFocus = (m.loginFocus + 1) % lN
			m.focusLoginInput()
		case "shift+tab", "up":
			m.loginFocus = (m.loginFocus + lN - 1) % lN
			m.focusLoginInput()
		case "enter":
			if m.loginFocus < lN-1 {
				m.loginFocus++
				m.focusLoginInput()
			} else {
				return m.doLogin()
			}
		}

	case loginOKMsg:
		m.cfg.Token = msg.token
		m.cfg.UserLogin = msg.login
		m.api = NewServiceClient(m.loginInputs[lURL].Value(), msg.token)
		_ = SaveConfig(m.cfg)
		m.screen = screenMain
		m.appendLog("Загрузка устройств…")
		updates := m.updates
		cli := m.api
		go func() {
			devices, err := cli.GetDevices()
			if err != nil {
				updates <- devicesMsg{err: err.Error()}
			} else {
				updates <- devicesMsg{devices: devices}
			}
		}()
		return m, m.waitForBg()

	case loginErrMsg:
		m.loginStatus = msg.err
		m.loginIsErr = true
		return m, m.waitForBg()
	}

	// Update focused input
	var cmd tea.Cmd
	m.loginInputs[m.loginFocus], cmd = m.loginInputs[m.loginFocus].Update(msg)
	cmds = append(cmds, cmd)
	return m, tea.Batch(cmds...)
}

func (m *Model) focusLoginInput() {
	for i := range m.loginInputs {
		m.loginInputs[i].Blur()
	}
	m.loginInputs[m.loginFocus].Focus()
}

func (m Model) doLogin() (tea.Model, tea.Cmd) {
	url := strings.TrimSpace(m.loginInputs[lURL].Value())
	user := strings.TrimSpace(m.loginInputs[lUser].Value())
	pass := m.loginInputs[lPass].Value()
	if user == "" || pass == "" {
		m.loginStatus = "Введите имя пользователя и пароль"
		m.loginIsErr = true
		return m, m.waitForBg()
	}
	m.loginStatus = "Подключение…"
	m.loginIsErr = false

	updates := m.updates
	go func() {
		cli := NewServiceClient(url, "")
		token, err := cli.Login(url, user, pass)
		if err != nil {
			updates <- loginErrMsg{err.Error()}
		} else {
			updates <- loginOKMsg{token, user}
		}
	}()
	return m, m.waitForBg()
}

// ---- Main ----

func (m Model) updateMain(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.termW, m.termH = msg.Width, msg.Height

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			if m.running {
				m.requestStop()
			}
			return m, tea.Quit
		case "s", "enter":
			if !m.running {
				return m.startPositioning()
			}
		case "x":
			if m.running {
				m.requestStop()
			}
		}

	// Background messages
	case posMsg:
		m.lat, m.lon, m.height = msg.lat, msg.lon, msg.height
		m.quality, m.nsat = msg.quality, msg.nsat
		m.convergence = msg.convergence
		m.isMoving = msg.isMoving
		// Как только rtkrcv сигнализирует сходимость — скрываем кандидата из UI.
		if msg.convergence == 1 {
			m.candidateTxt = "—"
		}
		cmds = append(cmds, m.waitForBg())

	case candidateMsg:
		m.candidateTxt = fmt.Sprintf("%s:%d", msg.ip, msg.port)
		m.appendLog(fmt.Sprintf("Кандидат: %s:%d  движется=%v", msg.ip, msg.port, msg.isMoving))
		cmds = append(cmds, m.waitForBg())

	case logMsg:
		m.appendLog(msg.text)
		cmds = append(cmds, m.waitForBg())

	case modeMsg:
		m.solverMode = msg.mode
		cmds = append(cmds, m.waitForBg())

	case ephMsg:
		m.ephAddr = msg.addr
		cmds = append(cmds, m.waitForBg())

	case ssrMsg:
		m.ssrAddr = msg.addr
		cmds = append(cmds, m.waitForBg())

	case devicesMsg:
		if msg.err != "" {
			m.appendLog(styleErr.Render("Устройства: " + msg.err))
		} else {
			for i := range msg.devices {
				if msg.devices[i].DeviceType == "gnss_receiver" {
					d := msg.devices[i]
					m.device = &d
					break
				}
			}
			if m.device == nil {
				m.appendLog(styleErr.Render("Нет ГНСС-устройства — добавьте на сайте сервиса"))
			} else {
				ant := m.device.AntennaName
				if ant == "" {
					ant = "*"
				}
				m.appendLog(fmt.Sprintf("Устройство: %s  антенна: %s", m.device.Name, ant))
			}
		}
		cmds = append(cmds, m.waitForBg())

	case portMsg:
		m.outputPort = msg.port
		cmds = append(cmds, m.waitForBg())

	case stoppedMsg:
		m.running = false
		m.solverMode = "—"
		m.candidateTxt = "None"
		m.outputPort = 0
		m.appendLog("Позиционирование остановлено")
		cmds = append(cmds, m.waitForBg())

	default:
		cmds = append(cmds, m.waitForBg())
	}

	return m, tea.Batch(cmds...)
}

func (m *Model) appendLog(text string) {
	ts := time.Now().Format("15:04:05")
	m.logs = append(m.logs, fmt.Sprintf("[%s] %s", ts, text))
	if len(m.logs) > m.maxLogs {
		m.logs = m.logs[len(m.logs)-m.maxLogs:]
	}
}

func (m *Model) requestStop() {
	if m.stopChan != nil {
		select {
		case m.stopChan <- struct{}{}:
		default:
		}
	}
}

// ================================================================
// Positioning goroutine
// ================================================================

func (m Model) startPositioning() (Model, tea.Cmd) {
	if m.cfg.RtkrcvPath == "" {
		m.appendLog(styleErr.Render("rtkrcv не настроен — укажите путь в конфиге"))
		return m, nil
	}
	if m.device == nil {
		m.appendLog(styleErr.Render("Устройство не выбрано — добавьте ГНСС-устройство на сайте сервиса"))
		return m, nil
	}

	roverType := m.device.ReceiverType
	if roverType == "" {
		roverType = "tcp"
	}
	roverPath := m.device.ReceiverPath()
	ant := AntennaCfg{
		Type: m.device.AntennaName,
		E:    m.device.AntennaE,
		N:    m.device.AntennaN,
		U:    m.device.AntennaU,
	}

	m.running = true
	m.stopChan = make(chan struct{}, 1)
	m.appendLog("Запуск позиционирования…")

	// Capture what we need for the goroutine
	cfg := m.cfg
	api := m.api
	updates := m.updates
	stopCh := m.stopChan

	send := func(v tea.Msg) {
		select {
		case updates <- v:
		default:
		}
	}
	log := func(s string) { send(logMsg{s}) }

	go func() {
		defer send(stoppedMsg{})

		// internalPort — CPP logstr1 raw RTCM3 от приёмника (внутренний, читает RTCMProxy)
		// publicPort   — RTCMProxy: патченный RTCM3 с CPP-координатами базы (сообщается сервису)
		// solutionPort — SolutionServer: portTCP-текст с реальными SD из solution.pos
		// injectPort   — InjectRelay: CPP inpstr4 (-port) подключается сюда;
		//                relay читает solution-текст кандидата и шлёт его в rtkrcv
		internalPort, err := freePort()
		if err != nil {
			internalPort = cfg.OutputPort
		}
		publicPort, _ := freePort()
		solutionPort, _ := freePort()
		injectPort, _ := freePort()
		send(portMsg{publicPort})

		proxy := newRTCMProxy(publicPort, internalPort)
		go proxy.Serve(stopCh)

		solServer := newSolutionServer(solutionPort)
		go solServer.Serve(stopCh)

		relay := newInjectRelay(injectPort)

		// candidateStop закрывается когда CPP сошлось — relay прекращает слать RTK_sol.
		// Создаётся заново при каждом назначении кандидата.
		var candidateStop chan struct{}
		stopCandidate := func() {
			if candidateStop != nil {
				select {
				case <-candidateStop: // already closed
				default:
					close(candidateStop)
				}
				candidateStop = nil
			}
		}
		defer stopCandidate()

		// newRTKSolver создаёт Solver с правильным configsDir, без outputPort/injectPort.
		newRTKSolver := func() *Solver {
			s := NewSolver(cfg.RtkrcvPath, cfg.ATXFile, cfg.WorkDir)
			if cfg.RtkrcvPath != "" {
				cfgDir := filepath.Join(filepath.Dir(cfg.RtkrcvPath), "..", "configs")
				if abs, err2 := filepath.Abs(cfgDir); err2 == nil {
					if fi, errS := os.Stat(abs); errS == nil && fi.IsDir() {
						s.SetConfigsDir(abs)
					}
				}
			}
			return s
		}

		solver := NewSolver(cfg.RtkrcvPath, cfg.ATXFile, cfg.WorkDir)
		solver.SetOutputPort(internalPort)
		solver.SetInjectPort(injectPort)
		// configs лежат рядом с rtkrcv: .../cmd/solver/app/ -> .../cmd/solver/configs/
		if cfg.RtkrcvPath != "" {
			candidate := filepath.Join(filepath.Dir(cfg.RtkrcvPath), "..", "configs")
			if abs, err := filepath.Abs(candidate); err == nil {
				if fi, err := os.Stat(abs); err == nil && fi.IsDir() {
					solver.SetConfigsDir(abs)
				}
			}
		}

		go relay.Serve(stopCh)

		mover := NewMovingDetector(5.0, 30.0)
		var regResp *RegisterOnlineResp
		var candidateAssigned bool

		// 1. Public IP
		log("Определение публичного IP…")
		publicIP := GetPublicIP()
		if publicIP == "" {
			log("Публичный IP не определён, используется 0.0.0.0")
			publicIP = "0.0.0.0"
		} else {
			log(fmt.Sprintf("Публичный IP: %s", publicIP))
		}

		// 2. Register with service to get EPH/SSR server addresses
		// Position is unknown at this point — send zeroes, will be updated after first fix
		log("Регистрация на сервисе…")
		regResp, regErr := api.RegisterSession(publicIP, publicPort, solutionPort, 0, 0, 0)
		err = regErr
		ephHost, ephPort := "localhost", 9200
		ssrHost, ssrPort := "localhost", 9201
		if err != nil {
			log(fmt.Sprintf("Регистрация не удалась (%v) — офлайн-режим", err))
		} else {
			ephHost, ephPort = regResp.EPHHost, regResp.EPHPort
			ssrHost, ssrPort = regResp.SSRHost, regResp.SSRPort
			send(ephMsg{fmt.Sprintf("%s:%d", ephHost, ephPort)})
			send(ssrMsg{fmt.Sprintf("%s:%d", ssrHost, ssrPort)})
			log(fmt.Sprintf("Сервис: EPH %s:%d  SSR %s:%d", ephHost, ephPort, ssrHost, ssrPort))
		}

		// 3. Start CPP-Kinematic with server EPH/SSR streams
		log("Запуск CPP-Kinematic…")
		if err := solver.StartCPPKinematic(roverType, roverPath, ephHost, ephPort, ssrHost, ssrPort, ant); err != nil {
			log(fmt.Sprintf("Ошибка запуска rtkrcv: %v", err))
			solver.Stop()
			if regResp != nil {
				_ = api.DeleteSession()
			}
			return
		}
		defer solver.Stop()
		send(modeMsg{"CPP-Kinematic"})
		log(fmt.Sprintf("rtkrcv запущен → EPH %s:%d  SSR %s:%d", ephHost, ephPort, ssrHost, ssrPort))
		if cfgPath := solver.LastCfgFile(); cfgPath != "" {
			log(fmt.Sprintf("Конфиг: %s", cfgPath))
		}
		// Сообщаем SolutionServer путь к файлу решения, чтобы он мог начать читать его.
		if solFile := solver.LastSolFile(); solFile != "" {
			solServer.SetSolutionFile(solFile)
		}

		// 4. Wait for first converged fix; display all positions as they arrive
		log("Ожидание первого фикса…")
		var firstPos *Position
		for i := 0; i < 300; i++ {
			select {
			case <-stopCh:
				if regResp != nil {
					_ = api.DeleteSession()
				}
				return
			case <-time.After(time.Second):
			}
			p := solver.GetLatestPosition()
			conv := 0
			if p != nil && p.Converged() {
				conv = 1
			}
			// Каждые 15 с обновляем статус на сервере и ищем кандидата.
			// Кандидата ищем только пока решение ещё не сошлось.
			if regResp != nil && i%15 == 0 {
				rLat, rLon := 0.0, 0.0
				if firstPos != nil {
					rLat, rLon, _ = Roughen(firstPos.Lat, firstPos.Lon, firstPos.Height, cfg.UserLogin)
				}
				if cand, err := api.UpdateStatus(conv, false, rLat, rLon); err == nil && cand != nil && !candidateAssigned && conv == 0 {
					candidateAssigned = true
					send(candidateMsg{cand.IP, cand.Port, cand.IsMoving})
					candidateStop = make(chan struct{})
					merged := mergeStop(stopCh, candidateStop)
					// Если кандидат на том же хосте — избегаем NAT-петли, используем loopback.
					candIP := cand.IP
					if candIP == publicIP {
						candIP = "127.0.0.1"
					}
					rtkS := newRTKSolver()
					if cand.IsMoving {
						log(fmt.Sprintf("Инжекция CPP + Moving Base → база %s:%d", candIP, cand.Port))
						send(modeMsg{"CPP + Moving Base"})
						if errMB := rtkS.StartMB(roverType, roverPath, candIP, cand.Port, ephHost, ephPort, ssrHost, ssrPort, ant); errMB != nil {
							log(fmt.Sprintf("MB solver ошибка: %v", errMB))
						} else if sf := rtkS.LastSolFile(); sf != "" {
							go relay.ForwardFromRTKFile(sf, merged)
						}
					} else {
						// Запускаем параллельный RTK-решатель: кандидат = базовая станция.
						// Координаты базы приходят через RTCMProxy кандидата (RTCM 1005/1006
						// содержит CPP-оценку кандидата). RTK fix инжектируется в CPP-фильтр.
						log(fmt.Sprintf("Инжекция CPP + RTK → база %s:%d", candIP, cand.Port))
						send(modeMsg{"CPP + RTK"})
						if errRTK := rtkS.StartRTK(roverType, roverPath, candIP, cand.Port, ephHost, ephPort, ssrHost, ssrPort, ant); errRTK != nil {
							log(fmt.Sprintf("RTK solver ошибка: %v", errRTK))
						} else if sf := rtkS.LastSolFile(); sf != "" {
							go relay.ForwardFromRTKFile(sf, merged)
						}
					}
					go func() { <-merged; rtkS.Stop() }()
				}
			}
			if p != nil {
				if conv == 1 {
					proxy.UpdatePos(p.Lat, p.Lon, p.Height)
					if candidateStop != nil {
						log("CPP сошлось — кандидат отключён")
						send(modeMsg{"CPP-Kinematic"})
						stopCandidate()
					}
				}
				send(posMsg{
					lat: p.Lat, lon: p.Lon, height: p.Height,
					quality: p.Quality, nsat: p.NSat,
					convergence: conv,
				})
				if p.Quality > 0 && firstPos == nil {
					firstPos = p
					log(fmt.Sprintf("Первый фикс: %.5f  %.5f  Q=%s", p.Lat, p.Lon, p.QualityLabel()))
				}
			}
		}
		if firstPos == nil {
			log("Фикс не получен за 5 мин — продолжаем")
		}

		// 6. Main loop
		ticker := time.NewTicker(time.Second)
		statusTicker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		defer statusTicker.Stop()

		for {
			select {
			case <-stopCh:
				if regResp != nil {
					_ = api.DeleteSession()
				}
				return

			case <-ticker.C:
				pos := solver.GetLatestPosition()
				if pos != nil {
					moving := mover.Update(pos.Lat, pos.Lon)
					conv := 0
					if pos.Converged() {
						conv = 1
						proxy.UpdatePos(pos.Lat, pos.Lon, pos.Height)
						if candidateStop != nil {
							log("CPP сошлось — кандидат отключён")
							send(modeMsg{"CPP-Kinematic"})
							stopCandidate()
						}
					}
					send(posMsg{
						lat: pos.Lat, lon: pos.Lon, height: pos.Height,
						quality: pos.Quality, nsat: pos.NSat,
						convergence: conv,
						isMoving:    moving,
					})
				}

			case <-statusTicker.C:
				if regResp == nil {
					continue
				}
				pos := solver.GetLatestPosition()
				if pos == nil {
					continue
				}
				moving := mover.Update(pos.Lat, pos.Lon)
				conv := 0
				if pos.Converged() {
					conv = 1
				}
				rLat, rLon, _ := Roughen(pos.Lat, pos.Lon, pos.Height, cfg.UserLogin)
				candidate, err := api.UpdateStatus(conv, moving, rLat, rLon)
				if err != nil {
					log(fmt.Sprintf("Обновление статуса: %v", err))
					continue
				}
				// Кандидата назначаем только пока решение не сошлось.
				if candidate != nil && !candidateAssigned && conv == 0 {
					candidateAssigned = true
					send(candidateMsg{candidate.IP, candidate.Port, candidate.IsMoving})
					candidateStop = make(chan struct{})
					merged := mergeStop(stopCh, candidateStop)
					// Если кандидат на том же хосте — избегаем NAT-петли, используем loopback.
					candIP := candidate.IP
					if candIP == publicIP {
						candIP = "127.0.0.1"
					}
					rtkS := newRTKSolver()
					if candidate.IsMoving {
						log(fmt.Sprintf("Инжекция CPP + Moving Base → база %s:%d", candIP, candidate.Port))
						send(modeMsg{"CPP + Moving Base"})
						if errMB := rtkS.StartMB(roverType, roverPath, candIP, candidate.Port, ephHost, ephPort, ssrHost, ssrPort, ant); errMB != nil {
							log(fmt.Sprintf("MB solver ошибка: %v", errMB))
						} else if sf := rtkS.LastSolFile(); sf != "" {
							go relay.ForwardFromRTKFile(sf, merged)
						}
					} else {
						log(fmt.Sprintf("Инжекция CPP + RTK → база %s:%d", candIP, candidate.Port))
						send(modeMsg{"CPP + RTK"})
						if errRTK := rtkS.StartRTK(roverType, roverPath, candIP, candidate.Port, ephHost, ephPort, ssrHost, ssrPort, ant); errRTK != nil {
							log(fmt.Sprintf("RTK solver ошибка: %v", errRTK))
						} else if sf := rtkS.LastSolFile(); sf != "" {
							go relay.ForwardFromRTKFile(sf, merged)
						}
					}
					go func() { <-merged; rtkS.Stop() }()
				}
			}
		}
	}()

	return m, m.waitForBg()
}

// ================================================================
// View
// ================================================================

func (m Model) View() string {
	if m.termW == 0 {
		return ""
	}
	switch m.screen {
	case screenLogin:
		return m.loginView()
	case screenMain:
		return m.mainView()
	}
	return ""
}

// ---- Login view ----

func (m Model) loginView() string {
	boxW := 62
	if m.termW < boxW+4 {
		boxW = m.termW - 4
	}
	iW := boxW - 6

	for i := range m.loginInputs {
		m.loginInputs[i].Width = iW
	}

	title := styleTitle.Width(boxW - 4).Align(lipgloss.Center).
		Render("Collaborative Positioning Client")

	var statusLine string
	if m.loginStatus != "" {
		if m.loginIsErr {
			statusLine = styleErr.Render(m.loginStatus)
		} else {
			statusLine = styleWarn.Render(m.loginStatus)
		}
	}

	loginBtn := lipgloss.NewStyle().
		Background(lipgloss.Color("62")).
		Foreground(lipgloss.Color("15")).
		Padding(0, 2).Bold(true).
		Render("  Войти  ")

	body := strings.Join([]string{
		title, "",
		styleLabel.Render("Service URL"),
		m.loginInputs[lURL].View(),
		styleLabel.Render("Имя пользователя"),
		m.loginInputs[lUser].View(),
		styleLabel.Render("Пароль"),
		m.loginInputs[lPass].View(),
		"",
		loginBtn,
		"",
		statusLine,
		"",
		styleLabel.Render("Tab — следующее поле   Enter — войти   q — выход"),
	}, "\n")

	box := styleBorder.Width(boxW).Padding(1, 2).Render(body)

	vpad := (m.termH - strings.Count(box, "\n") - 2) / 2
	if vpad < 0 {
		vpad = 0
	}
	hpad := (m.termW - boxW - 4) / 2
	if hpad < 0 {
		hpad = 0
	}

	return lipgloss.NewStyle().
		PaddingTop(vpad).
		PaddingLeft(hpad).
		Render(box)
}

// ---- Main view ----

func (m Model) mainView() string {
	totalW := m.termW
	if totalW < 80 {
		totalW = 80
	}
	panelW := (totalW - 4) / 3

	recv := m.devicePanel(panelW)
	pos := m.positionPanel(panelW)
	svc := m.servicePanel(totalW - 2*panelW - 6)

	top := lipgloss.JoinHorizontal(lipgloss.Top, recv, pos, svc)
	log := m.logPanel(totalW - 2)

	hint := styleLabel.Render("s=старт  x=стоп  q=выход")

	return lipgloss.JoinVertical(lipgloss.Left, top, log, hint)
}

func panelBox(w int, title string, rows []string) string {
	t := styleTitle.Width(w - 2).Align(lipgloss.Center).Render(title)
	body := strings.Join(append([]string{t, ""}, rows...), "\n")
	return styleBorder.Width(w).Render(body)
}

func row(label, value string) string {
	return fmt.Sprintf("%s %s", styleLabel.Render(label+":"), styleValue.Render(value))
}

func (m Model) devicePanel(w int) string {
	var rows []string
	if m.device == nil {
		rows = []string{
			styleErr.Render("Устройство не найдено"),
			"",
			styleLabel.Render("Добавьте ГНСС-устройство"),
			styleLabel.Render("на сайте сервиса"),
		}
	} else {
		d := m.device
		rcvType := d.ReceiverType
		if rcvType == "" {
			rcvType = "tcp"
		}
		var addr string
		switch rcvType {
		case "ntrip":
			addr = fmt.Sprintf("%s:%d/%s", d.ReceiverHost, d.ReceiverPort, d.ReceiverMount)
		case "serial":
			addr = fmt.Sprintf("%s @ %d", d.ReceiverHost, d.ReceiverPort)
		default:
			addr = fmt.Sprintf("%s:%d", d.ReceiverHost, d.ReceiverPort)
		}
		ant := d.AntennaName
		if ant == "" {
			ant = "*"
		}
		antOff := fmt.Sprintf("E%.3f N%.3f U%.3f", d.AntennaE, d.AntennaN, d.AntennaU)
		rows = []string{
			row("Устройство", d.Name),
			row("Подключение", strings.ToUpper(rcvType)),
			row("Адрес", addr),
			"",
			row("Антенна", ant),
			row("Смещение", antOff),
		}
	}
	rows = append(rows, "")
	if !m.running {
		rows = append(rows, styleActive.Render("  [ s — Запустить ]  "))
	} else {
		rows = append(rows, styleErr.Render("  [ x — Остановить ]  "))
	}
	return panelBox(w, "УСТРОЙСТВО", rows)
}

func (m Model) positionPanel(w int) string {
	qual := "—"
	if m.quality > 0 {
		qual = qualityLabel[m.quality]
		if qual == "" {
			qual = fmt.Sprintf("%d", m.quality)
		}
	}
	movTxt := "Нет"
	if m.isMoving {
		movTxt = styleWarn.Render("Да")
	}
	convTxt := styleWarn.Render("○ В процессе")
	if m.convergence == 1 {
		convTxt = styleOK.Render("● Сошлось")
	}
	lat := "—"
	lon := "—"
	height := "—"
	if m.quality > 0 {
		lat = fmt.Sprintf("%.9f°", m.lat)
		lon = fmt.Sprintf("%.9f°", m.lon)
		height = fmt.Sprintf("%.4f м", m.height)
	}
	rows := []string{
		row("Широта", lat),
		row("Долгота", lon),
		row("Высота", height),
		"",
		row("Решение", qual),
		row("Спутники", fmt.Sprintf("%d", m.nsat)),
		"",
		row("Сходимость", convTxt),
		row("Движение", movTxt),
	}
	return panelBox(w, "ПОЗИЦИЯ", rows)
}

func (m Model) servicePanel(w int) string {
	eph := m.ephAddr
	if eph == "" {
		eph = "—"
	}
	ssr := m.ssrAddr
	if ssr == "" {
		ssr = "—"
	}
	svcStatus := styleOK.Render("● Подключён")
	if m.api == nil {
		svcStatus = styleErr.Render("○ Нет подключения")
	}
	port := "—"
	if m.outputPort > 0 {
		port = fmt.Sprintf("%d", m.outputPort)
	}

	rows := []string{
		row("Сервис", svcStatus),
		row("EPH поток", eph),
		row("SSR поток", ssr),
		"",
		row("Ваш RTCM порт", port),
		row("Режим решателя", m.solverMode),
		row("Кандидат", m.candidateTxt),
	}
	return panelBox(w, "СЕРВИС", rows)
}

func (m Model) logPanel(w int) string {
	last := 6
	start := 0
	if len(m.logs) > last {
		start = len(m.logs) - last
	}
	lines := make([]string, 0, last)
	for _, l := range m.logs[start:] {
		lines = append(lines, styleLabel.Render(l))
	}
	body := styleTitle.Width(w-2).Render("ЛОГ") + "\n" + strings.Join(lines, "\n")
	return styleBorder.Width(w).Render(body)
}
