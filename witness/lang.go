package main

import "strings"

// console holds what the program writes in its own window. The page has its own strings (ui/strings.json).
type console struct {
	Title        string
	Tagline      string
	Running      string
	KeepOpen     string
	Already      string
	Busy         string
	MissingNode  string
	WrongNode    string
	NoDataFolder string // %v: the error
	StartFailed  string // %v: the error
	Stopping     string
	Stopped      string // the node's own message, if any, follows on the next line
	PressEnter   string
}

var consoleTexts = map[string]console{
	"en": {
		Title:        "Ark-0 Witness",
		Tagline:      "Ark-0 is a test network for trying things out. It may be reset at any time.",
		Running:      "The witness is running. Its page is open in your browser:",
		KeepOpen:     "Keep this window open. Closing it stops the node.",
		Already:      "The witness is already running in another window. Its page:",
		Busy:         "Another program is using the witness's data folder. Close it and try again.",
		MissingNode:  "bitcoind.exe was not found next to Ark0Witness.exe. Unzip the whole download into a folder first, then double-click Ark0Witness.exe in that folder.",
		WrongNode:    "bitcoind.exe in this folder is not the one this witness was built with. Download the witness again.",
		NoDataFolder: "The data folder cannot be created: %v",
		StartFailed:  "The witness cannot start: %v",
		Stopping:     "Stopping the node…",
		Stopped:      "The node has stopped.",
		PressEnter:   "Press Enter to close this window.",
	},
	"zh": {
		Title:        "方舟 0 号见证器",
		Tagline:      "方舟 0 号是测试网，仅供测试体验，随时可能重置。",
		Running:      "见证器正在运行，状态页已在浏览器中打开：",
		KeepOpen:     "请不要关闭这个窗口。关闭它，节点就会停下。",
		Already:      "见证器已经在另一个窗口里运行，它的状态页：",
		Busy:         "有别的程序正在使用见证器的数据文件夹。请关掉它再试。",
		MissingNode:  "在 Ark0Witness.exe 旁边找不到 bitcoind.exe。请先把下载的压缩包整个解压到一个文件夹，再双击那个文件夹里的 Ark0Witness.exe。",
		WrongNode:    "这个文件夹里的 bitcoind.exe 不是见证器配套的那一个。请重新下载见证器。",
		NoDataFolder: "无法创建数据文件夹：%v",
		StartFailed:  "见证器无法启动：%v",
		Stopping:     "正在停止节点……",
		Stopped:      "节点已停止。",
		PressEnter:   "按回车键关闭这个窗口。",
	},
	"ja": {
		Title:        "Ark-0 ウィットネス",
		Tagline:      "Ark-0 はテストネットです。試用のためのもので、いつでもリセットされる可能性があります。",
		Running:      "ウィットネスが動いています。状態ページをブラウザーで開きました：",
		KeepOpen:     "このウィンドウは開いたままにしてください。閉じるとノードが停止します。",
		Already:      "ウィットネスは別のウィンドウですでに動いています。状態ページ：",
		Busy:         "別のプログラムがウィットネスのデータフォルダーを使っています。そのプログラムを閉じてから、もう一度お試しください。",
		MissingNode:  "Ark0Witness.exe と同じ場所に bitcoind.exe が見つかりません。ダウンロードしたファイルをまずフォルダーにすべて展開し、そのフォルダー内の Ark0Witness.exe をダブルクリックしてください。",
		WrongNode:    "このフォルダーの bitcoind.exe は、このウィットネスと一緒に配布されたものではありません。ウィットネスをダウンロードし直してください。",
		NoDataFolder: "データフォルダーを作成できません：%v",
		StartFailed:  "ウィットネスを起動できません：%v",
		Stopping:     "ノードを停止しています…",
		Stopped:      "ノードが停止しました。",
		PressEnter:   "Enter キーを押すと、このウィンドウを閉じます。",
	},
	"ko": {
		Title:        "Ark-0 위트니스",
		Tagline:      "Ark-0은 테스트넷입니다. 체험용이며 언제든 초기화될 수 있습니다.",
		Running:      "위트니스가 실행 중입니다. 브라우저에서 상태 페이지를 열었습니다:",
		KeepOpen:     "이 창을 닫지 마세요. 창을 닫으면 노드가 멈춥니다.",
		Already:      "위트니스가 이미 다른 창에서 실행 중입니다. 상태 페이지:",
		Busy:         "다른 프로그램이 위트니스의 데이터 폴더를 사용하고 있습니다. 그 프로그램을 닫고 다시 시도하세요.",
		MissingNode:  "Ark0Witness.exe 옆에 bitcoind.exe가 없습니다. 내려받은 압축 파일을 먼저 폴더에 모두 풀고, 그 폴더의 Ark0Witness.exe를 더블클릭하세요.",
		WrongNode:    "이 폴더의 bitcoind.exe는 이 위트니스와 함께 배포된 파일이 아닙니다. 위트니스를 다시 내려받으세요.",
		NoDataFolder: "데이터 폴더를 만들 수 없습니다: %v",
		StartFailed:  "위트니스를 시작할 수 없습니다: %v",
		Stopping:     "노드를 멈추는 중입니다…",
		Stopped:      "노드가 멈췄습니다.",
		PressEnter:   "Enter 키를 누르면 이 창이 닫힙니다.",
	},
	"ru": {
		Title:        "Ark-0 Witness",
		Tagline:      "Ark-0 — тестовая сеть: только для пробы, её могут сбросить в любой момент.",
		Running:      "Witness работает. Страница состояния открыта в браузере:",
		KeepOpen:     "Не закрывайте это окно: если его закрыть, узел остановится.",
		Already:      "Witness уже работает в другом окне. Его страница:",
		Busy:         "Папку данных Witness занимает другая программа. Закройте её и попробуйте снова.",
		MissingNode:  "Рядом с Ark0Witness.exe нет bitcoind.exe. Сначала распакуйте весь скачанный архив в папку, затем дважды щёлкните Ark0Witness.exe в этой папке.",
		WrongNode:    "bitcoind.exe в этой папке — не тот, с которым собран этот Witness. Скачайте Witness заново.",
		NoDataFolder: "Не удаётся создать папку данных: %v",
		StartFailed:  "Witness не может запуститься: %v",
		Stopping:     "Останавливаем узел…",
		Stopped:      "Узел остановился.",
		PressEnter:   "Нажмите Enter, чтобы закрыть это окно.",
	},
	"es": {
		Title:        "Ark-0 Witness",
		Tagline:      "Ark-0 es una red de pruebas, solo para probar; puede reiniciarse en cualquier momento.",
		Running:      "Ark-0 Witness está en marcha. Su página se ha abierto en el navegador:",
		KeepOpen:     "No cierres esta ventana: si la cierras, el nodo se detiene.",
		Already:      "Ark-0 Witness ya está en marcha en otra ventana. Su página:",
		Busy:         "Otro programa está usando la carpeta de datos de Ark-0 Witness. Ciérralo y vuelve a intentarlo.",
		MissingNode:  "No se encuentra bitcoind.exe junto a Ark0Witness.exe. Primero descomprime toda la descarga en una carpeta y luego haz doble clic en el Ark0Witness.exe de esa carpeta.",
		WrongNode:    "El bitcoind.exe de esta carpeta no es el que acompaña a este Ark-0 Witness. Vuelve a descargarlo.",
		NoDataFolder: "No se puede crear la carpeta de datos: %v",
		StartFailed:  "Ark-0 Witness no puede iniciarse: %v",
		Stopping:     "Deteniendo el nodo…",
		Stopped:      "El nodo se ha detenido.",
		PressEnter:   "Pulsa Intro para cerrar esta ventana.",
	},
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
