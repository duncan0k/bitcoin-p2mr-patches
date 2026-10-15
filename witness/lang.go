package main

import "strings"

// console holds what the program writes in its own window. The page has its own strings (ui/strings.json).
type console struct {
	Title            string
	Tagline          string
	Running          string
	RunningNoBrowser string
	Leftover         string
	KeepOpen         string
	Already          string
	AlreadyElsewhere string
	Busy             string
	MissingNode      string
	MissingNodeLinux string
	WrongNode        string
	NoDataFolder     string // %v: the error
	StartFailed      string // %v: the error
	Stopping         string
	Stopped          string // the node's own message, if any, follows on the next line
	PressEnter       string
}

var consoleTexts = map[string]console{
	"en": {
		Title:            "Ark-0 Witness",
		Tagline:          "Ark-0 is a test network for trying things out. It may be reset at any time.",
		Running:          "The witness is running. Its page is open in your browser:",
		RunningNoBrowser: "The witness is running. Open its page in a browser on this computer:",
		Leftover:         "Stopping a node left from an earlier run…",
		KeepOpen:         "Keep this window open. Closing it stops the node.",
		Already:          "The witness is already running in another window. Its page:",
		AlreadyElsewhere: "The witness is already running in another window, which shows the address of its page.",
		Busy:             "Another program is using the witness's data folder. Close it and try again.",
		MissingNode:      "{node} was not found next to {program}. Unzip the whole download into a folder first, then double-click {program} in that folder.",
		MissingNodeLinux: "{node} was not found next to {program}. Unpack the whole download into a folder first, then run {program} in that folder.",
		WrongNode:        "{node} in this folder is not the one this witness was built with. Download the witness again.",
		NoDataFolder:     "The data folder cannot be created: %v",
		StartFailed:      "The witness cannot start: %v",
		Stopping:         "Stopping the node…",
		Stopped:          "The node has stopped.",
		PressEnter:       "Press Enter to close this window.",
	},
	"zh": {
		Title:            "方舟 0 号见证器",
		Tagline:          "方舟 0 号是测试网，仅供测试体验，随时可能重置。",
		Running:          "见证器正在运行，状态页已在浏览器中打开：",
		RunningNoBrowser: "见证器正在运行，请在这台电脑的浏览器中打开状态页：",
		Leftover:         "正在停止上次运行留下的节点……",
		KeepOpen:         "请不要关闭这个窗口。关闭它，节点就会停下。",
		Already:          "见证器已经在另一个窗口里运行，它的状态页：",
		AlreadyElsewhere: "见证器已经在另一个窗口里运行，那个窗口里显示着它的状态页地址。",
		Busy:             "有别的程序正在使用见证器的数据文件夹。请关掉它再试。",
		MissingNode:      "在 {program} 旁边找不到 {node}。请先把下载的压缩包整个解压到一个文件夹，再双击那个文件夹里的 {program}。",
		MissingNodeLinux: "在 {program} 旁边找不到 {node}。请先把下载的压缩包整个解压到一个文件夹，再在那个文件夹里运行 {program}。",
		WrongNode:        "这个文件夹里的 {node} 不是见证器配套的那一个。请重新下载见证器。",
		NoDataFolder:     "无法创建数据文件夹：%v",
		StartFailed:      "见证器无法启动：%v",
		Stopping:         "正在停止节点……",
		Stopped:          "节点已停止。",
		PressEnter:       "按回车键关闭这个窗口。",
	},
	"ja": {
		Title:            "Ark-0 ウィットネス",
		Tagline:          "Ark-0 は試してみるためのテストネットワークです。いつでもリセットされる可能性があります。",
		Running:          "ウィットネスが動いています。状態ページをブラウザーで開きました：",
		RunningNoBrowser: "ウィットネスが動いています。このコンピューターのブラウザーで状態ページを開いてください：",
		Leftover:         "前回の実行で残ったノードを停止しています…",
		KeepOpen:         "このウィンドウは開いたままにしてください。閉じるとノードが停止します。",
		Already:          "ウィットネスは別のウィンドウですでに動いています。状態ページ：",
		AlreadyElsewhere: "ウィットネスは別のウィンドウですでに動いています。状態ページのアドレスは、そのウィンドウに表示されています。",
		Busy:             "別のプログラムがウィットネスのデータフォルダーを使っています。そのプログラムを閉じてから、もう一度お試しください。",
		MissingNode:      "{program} と同じ場所に {node} が見つかりません。ダウンロードしたファイルをまずフォルダーにすべて展開し、そのフォルダー内の {program} をダブルクリックしてください。",
		MissingNodeLinux: "{program} と同じ場所に {node} が見つかりません。ダウンロードしたファイルをまずフォルダーにすべて展開し、そのフォルダーで {program} を実行してください。",
		WrongNode:        "このフォルダーの {node} は、このウィットネスと一緒に配布されたものではありません。ウィットネスをダウンロードし直してください。",
		NoDataFolder:     "データフォルダーを作成できません：%v",
		StartFailed:      "ウィットネスを起動できません：%v",
		Stopping:         "ノードを停止しています…",
		Stopped:          "ノードが停止しました。",
		PressEnter:       "Enter キーを押すと、このウィンドウを閉じます。",
	},
	"ko": {
		Title:            "Ark-0 위트니스",
		Tagline:          "Ark-0 테스트 네트워크는 체험용이며, 언제든지 초기화될 수 있습니다.",
		Running:          "위트니스가 실행 중입니다. 브라우저에서 상태 페이지를 열었습니다:",
		RunningNoBrowser: "위트니스가 실행 중입니다. 이 컴퓨터의 브라우저에서 상태 페이지를 여세요:",
		Leftover:         "이전 실행에서 남은 노드를 멈추는 중입니다…",
		KeepOpen:         "이 창을 닫지 마세요. 창을 닫으면 노드가 멈춥니다.",
		Already:          "위트니스가 이미 다른 창에서 실행 중입니다. 상태 페이지:",
		AlreadyElsewhere: "위트니스가 이미 다른 창에서 실행 중입니다. 상태 페이지 주소는 그 창에 표시되어 있습니다.",
		Busy:             "다른 프로그램이 위트니스의 데이터 폴더를 사용하고 있습니다. 그 프로그램을 닫고 다시 시도하세요.",
		MissingNode:      "{program} 옆에 {node}가 없습니다. 내려받은 압축 파일을 먼저 폴더에 모두 풀고, 그 폴더의 {program}를 더블클릭하세요.",
		MissingNodeLinux: "{program} 옆에 {node}가 없습니다. 내려받은 압축 파일을 먼저 폴더에 모두 풀고, 그 폴더에서 {program}를 실행하세요.",
		WrongNode:        "이 폴더의 {node}는 이 위트니스와 함께 배포된 파일이 아닙니다. 위트니스를 다시 내려받으세요.",
		NoDataFolder:     "데이터 폴더를 만들 수 없습니다: %v",
		StartFailed:      "위트니스를 시작할 수 없습니다: %v",
		Stopping:         "노드를 멈추는 중입니다…",
		Stopped:          "노드가 멈췄습니다.",
		PressEnter:       "Enter 키를 누르면 이 창이 닫힙니다.",
	},
	"ru": {
		Title:            "Ark-0 Witness",
		Tagline:          "Ark-0 — тестовая сеть: только для пробы, её могут сбросить в любой момент.",
		Running:          "Witness работает. Страница состояния открыта в браузере:",
		RunningNoBrowser: "Witness работает. Откройте страницу состояния в браузере на этом компьютере:",
		Leftover:         "Останавливаем узел, оставшийся от прошлого запуска…",
		KeepOpen:         "Не закрывайте это окно: если его закрыть, узел остановится.",
		Already:          "Witness уже работает в другом окне. Его страница:",
		AlreadyElsewhere: "Witness уже работает в другом окне. Адрес его страницы показан в том окне.",
		Busy:             "Папку данных Witness занимает другая программа. Закройте её и попробуйте снова.",
		MissingNode:      "Рядом с {program} нет {node}. Сначала распакуйте весь скачанный архив в папку, затем дважды щёлкните {program} в этой папке.",
		MissingNodeLinux: "Рядом с {program} нет {node}. Сначала распакуйте весь скачанный архив в папку, затем запустите {program} в этой папке.",
		WrongNode:        "{node} в этой папке — не тот, с которым собран этот Witness. Скачайте Witness заново.",
		NoDataFolder:     "Не удаётся создать папку данных: %v",
		StartFailed:      "Witness не может запуститься: %v",
		Stopping:         "Останавливаем узел…",
		Stopped:          "Узел остановился.",
		PressEnter:       "Нажмите Enter, чтобы закрыть это окно.",
	},
	"es": {
		Title:            "Ark-0 Witness",
		Tagline:          "Ark-0 es una red de prueba para experimentar. Puede reiniciarse en cualquier momento.",
		Running:          "Ark-0 Witness está en marcha. Su página se ha abierto en el navegador:",
		RunningNoBrowser: "Ark-0 Witness está en marcha. Abre su página en un navegador de esta computadora:",
		Leftover:         "Deteniendo un nodo que quedó de una ejecución anterior…",
		KeepOpen:         "No cierres esta ventana: si la cierras, el nodo se detiene.",
		Already:          "Ark-0 Witness ya está en marcha en otra ventana. Su página:",
		AlreadyElsewhere: "Ark-0 Witness ya está en marcha en otra ventana, que muestra la dirección de su página.",
		Busy:             "Otro programa está usando la carpeta de datos de Ark-0 Witness. Ciérralo y vuelve a intentarlo.",
		MissingNode:      "No se encuentra {node} junto a {program}. Primero descomprime toda la descarga en una carpeta y luego haz doble clic en el {program} de esa carpeta.",
		MissingNodeLinux: "No se encuentra {node} junto a {program}. Primero descomprime toda la descarga en una carpeta y luego ejecuta {program} en esa carpeta.",
		WrongNode:        "El {node} de esta carpeta no es el que acompaña a este Ark-0 Witness. Vuelve a descargarlo.",
		NoDataFolder:     "No se puede crear la carpeta de datos: %v",
		StartFailed:      "Ark-0 Witness no puede iniciarse: %v",
		Stopping:         "Deteniendo el nodo…",
		Stopped:          "El nodo se ha detenido.",
		PressEnter:       "Presiona Enter para cerrar esta ventana.",
	},
}

// fillConsole keeps platform names out of the translations themselves.
func fillConsole(text console, platform string) console {
	text.MissingNode = fillProgramNames(platformText(text.MissingNode, text.MissingNodeLinux, platform), platform)
	text.MissingNodeLinux = fillProgramNames(text.MissingNodeLinux, platform)
	text.WrongNode = fillProgramNames(text.WrongNode, platform)
	return text
}

// pickLanguage is the first of the choices this program has, or English.
func pickLanguage(choices ...string) string {
	for _, c := range choices {
		if _, ok := consoleTexts[c]; ok {
			return c
		}
	}
	return "en"
}

// languageOfLocale maps a locale such as "zh_CN.UTF-8" or "ja-JP" to a language of this program, or "".
func languageOfLocale(locale string) string {
	lang := strings.ToLower(locale)
	if i := strings.IndexAny(lang, "_-.@"); i >= 0 {
		lang = lang[:i]
	}
	if _, ok := consoleTexts[lang]; ok {
		return lang
	}
	return ""
}

// languageOfLANGID maps a Windows language identifier to a language of this program, or "".
func languageOfLANGID(id uint16) string {
	switch id & 0x3ff {
	case 0x04:
		return "zh"
	case 0x09:
		return "en"
	case 0x0a:
		return "es"
	case 0x11:
		return "ja"
	case 0x12:
		return "ko"
	case 0x19:
		return "ru"
	}
	return ""
}
