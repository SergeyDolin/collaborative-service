// cmd/fagsbatch/main.go — batch PPP-AR для пунктов ФАГС через API РГС, сравнение с ГСК-2011
//
// Эталонные координаты: каталог ГСК-2011, преобразование в ITRF2020 через geocentric.xyz на эпоху наблюдений.
// PPP-результат: ITRF2020 (продукты CODE). Разности вычисляются в единой системе ITRF2020.
package main

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"collaborative/internal/services"

	"go.uber.org/zap"
)

// ── константы ─────────────────────────────────────────────────────────────────

const (
	serviceRoot     = "/Users/sergeidolin/collaborative-service"
	rtklibPath      = serviceRoot + "/cmd/solver/app"
	pppConfFile     = serviceRoot + "/cmd/solver/configs/ppp.conf"
	rgsAPIBaseHTTPS = "https://rgs.cgkipd.ru/api"
	rgsAPIBaseHTTP  = "http://rgs.cgkipd.ru/api"
)

// rgsAPIBase — текущая базовая точка API (устанавливается из флага).
var rgsAPIBase string

// ── каталог ФАГС (ГСК-2011, эпоха 2011.0) ────────────────────────────────────

type FAGSStation struct {
	Code    string
	City    string
	X, Y, Z float64 // геоцентрические координаты ГСК-2011, м
}

// Каталог: все пункты ФАГС (ГСК-2011, источник: rgs.cgkipd.ru/fags-coords).
var fagsCatalog = []FAGSStation{
	{"AGA1", "Александров Гай", 2712579.280, 3069874.600, 4872231.610},
	{"AMDR", "Амдерма", 1049947.710, 1948228.330, 5961852.910},
	{"ANDR", "Анадырь", -2727120.270, 118593.140, 5745237.760},
	{"ANTP", "Антипаюта", 518671.040, 2221799.950, 5936129.670},
	{"ARKH", "Архангельск", 2089120.320, 1785919.300, 5736350.060},
	{"AST3", "Астрахань", 2947614.930, 3280153.450, 4592467.830},
	{"AYN1", "Аян", -2632003.350, 2355326.520, 5293128.630},
	{"BARE", "Баренцбург", 1282847.960, 325537.340, 6218685.230},
	{"BELG", "Белгород", 3255206.200, 2415024.380, 4908424.400},
	{"BGHN", "Богучаны", -434615.290, 3323477.860, 5408356.610},
	{"BIRY", "Биряково", 2424821.990, 2144203.330, 5477567.630},
	{"BORO", "Бор", -1807.740, 3043431.240, 5586432.220},
	{"CHIT", "Чита", -1569449.830, 3606684.800, 5004896.590},
	{"CHRD", "Чокурдах", -1798055.350, 1128008.600, 5994470.450},
	{"CNG1", "Москва", 2846194.170, 2185227.730, 5255558.030},
	{"CRS1", "Черский", -2197112.610, 741975.870, 5921734.030},
	{"CRS2", "Черский", -2197111.030, 741974.530, 5921734.820},
	{"DKSN", "Диксон", 302702.630, 1790214.310, 6093796.290},
	{"EKTG", "Екатеринбург", 1710698.280, 3046082.210, 5318707.250},
	{"ELIS", "Элиста", 3158204.910, 3083193.320, 4589152.730},
	{"FSVO", "Свободный", -2463764.180, 3138579.240, 4959507.650},
	{"HES1", "Земля Франца-Иосифа", 551622.870, 885054.530, 6271269.540},
	{"HES2", "Земля Франца-Иосифа", 551623.430, 885048.570, 6271270.350},
	{"IRAE", "Ираёль", 1576795.150, 2267928.230, 5729887.090},
	{"IRKO", "Иркутск", -967362.510, 3786560.320, 5024207.790},
	{"ITKL", "Ытык-Кюёль", -2041478.780, 2146784.010, 5629620.050},
	{"ITRP", "Курильск", -3809500.530, 2390944.930, 4507273.920},
	{"IZVK", "Ижевск", 2082406.040, 2809821.240, 5316080.700},
	{"KAGP", "Красноярск", -178337.950, 3567385.910, 5266736.150},
	{"KARG", "Каргополь", 2373169.600, 1917127.560, 5582567.270},
	{"KERC", "Керчь", 3602113.050, 2678002.670, 4516245.070},
	{"KHAZ", "Хабаровск", -3004576.260, 2990039.980, 4749937.970},
	{"KIRV", "Киров", 2162144.730, 2538034.220, 5419254.700},
	{"KIZ1", "Кызыл", -307566.910, 3947896.900, 4984000.200},
	{"KLCH", "Ключи", -3348240.070, 1164276.330, 5284581.330},
	{"KLN1", "Калининград", 3460481.590, 1289437.650, 5182929.740},
	{"KLPS", "Колпашево", 412021.910, 3331878.920, 5404924.630},
	{"KNDL", "Кондоль", 2727978.100, 2733927.910, 5059142.360},
	{"KOTL", "Котлас", 2111995.860, 2235912.310, 5568913.960},
	{"KZDV", "Кузедеево", 187388.070, 3811848.970, 5093498.540},
	{"LOVJ", "Ловозеро", 1981439.940, 1367713.060, 5886956.460},
	{"LVR1", "Лаврентия", -2611834.310, -413315.880, 5784703.460},
	{"LVR2", "Лаврентия", -2611832.450, -413314.240, 5784704.400},
	{"MAG1", "Магадан", -2826664.780, 1579093.620, 5476846.770},
	{"MAMA", "Мамакан", -1385953.000, 3110631.250, 5375119.760},
	{"MBR1", "Северная Земля", -239739.880, 1165879.850, 6245108.520},
	{"MBR2", "Северная Земля", -239743.710, 1165889.080, 6245106.820},
	{"MFGS", "Мичуринский", 3159922.370, 2150016.250, 5089335.300},
	{"MGNS", "Магнитогорск", 1956159.630, 3272797.980, 5096333.020},
	{"MHCH", "Махачкала", 3157243.520, 3440577.710, 4329922.450},
	{"MKR1", "Новая Земля", 1173450.290, 1541288.680, 6056497.620},
	{"MKR2", "Новая Земля", 1173457.740, 1541267.610, 6056500.770},
	{"MOBJ", "Обнинск", 2936424.520, 2178374.110, 5208858.470},
	{"MURM", "Мурманск", 1923562.770, 1253583.860, 5930703.990},
	{"MYRN", "Мирный", -1197358.770, 2694536.530, 5637064.050},
	{"NARY", "Нарьян-Мар", 1464637.360, 1945079.100, 5875339.410},
	{"NERY", "Нерюнгри", -2001453.530, 2887681.900, 5306331.400},
	{"NIRO", "Ныроб", 1715719.410, 2613085.810, 5541095.530},
	{"NNOV", "Н.Новгород", 2550716.220, 2466143.150, 5282690.770},
	{"NOVG", "В.Новгород", 2852983.860, 1731971.010, 5417045.300},
	{"NOYA", "Ноябрьск", 724812.940, 2792377.810, 5669450.330},
	{"NRIL", "Норильск", 68529.220, 2255067.550, 5945836.140},
	{"NSK1", "Новосибирск", 447670.300, 3638117.390, 5202281.560},
	{"OHA1", "Оха", -3028241.850, 2285810.550, 5109806.390},
	{"OKTB", "Казань", 2363943.190, 2701430.350, 5254510.440},
	{"OLE1", "Олекминск", -1600930.180, 2726247.620, 5520990.550},
	{"OLE2", "Олекминск", -1600935.630, 2726245.390, 5520989.950},
	{"OLGA", "Ольга", -3280640.460, 3245168.680, 4388365.090},
	{"OLNK", "Оленёк", -896109.240, 2165755.570, 5912194.070},
	{"OMSR", "Омск", 1046504.430, 3511566.840, 5203198.460},
	{"ONGY", "Онгудай", 272557.170, 4035202.880, 4916374.920},
	{"OREN", "Оренбург", 2263462.030, 3244555.580, 4986384.010},
	{"OXTK", "Охотск", -2610468.950, 1950405.730, 5464521.780},
	{"PEVK", "Певек", -2187298.680, 375560.050, 5959534.270},
	{"PNGD", "Пангоды", 699009.960, 2521778.660, 5797118.950},
	{"POLO", "Полины Осипенко", -2826981.340, 2684995.010, 5030919.530},
	{"PRVM", "Первомайск", -3295110.890, 2458855.040, 4860087.780},
	{"PTGK", "Пятигорск", 3355880.040, 3135848.630, 4411457.100},
	{"PULJ", "Пулково", 2778952.890, 1625324.670, 5487678.400},
	{"RBCS", "Рубцовск", 607960.690, 3925423.270, 4973693.280},
	{"RGSK", "Ряжск", 2894146.850, 2438185.410, 5117137.090},
	{"RSTS", "Ростов-на-Дону", 3339359.230, 2771192.540, 4658757.690},
	{"RYR1", "Рыркайпий", -2301083.950, -21129.560, 5928617.530},
	{"RYR2", "Рыркайпий", -2301088.040, -21127.590, 5928615.970},
	{"SAMR", "Самара", 2447584.230, 2941190.810, 5085911.720},
	{"SEGE", "Сегежа", 2336393.520, 1592986.920, 5697993.630},
	{"SEMH", "Сеймчан", -2578537.290, 1348924.010, 5656804.990},
	{"SEV1", "Севастополь", 3794362.750, 2510336.280, 4455261.940},
	{"SEVR", "Северное", 715098.960, 3470739.550, 5285465.250},
	{"SLH1", "Салехард", 1011482.510, 2338107.050, 5827737.230},
	{"SPB2", "С-Петербург", 2767753.540, 1621331.270, 5494417.430},
	{"SVK1", "Северо-Курильск", -3704026.940, 1639969.470, 4910134.590},
	{"SVK2", "Северо-Курильск", -3704031.090, 1639971.030, 4910130.700},
	{"SYTO", "Сытомино", 984368.350, 2908582.210, 5571602.440},
	{"TILK", "Тиличики", -3062218.580, 758413.170, 5524836.970},
	{"TIXG", "Тикси", -1264873.310, 1569455.770, 6031003.410},
	{"TRH1", "Туруханск", 91954.740, 2620708.700, 5794651.050},
	{"TRH2", "Туруханск", 91957.960, 2620709.410, 5794650.680},
	{"TUAP", "Туапсе", 3563350.650, 2889411.240, 4416382.760},
	{"TULN", "Тулун", -684833.220, 3639754.710, 5175853.810},
	{"TURR", "Тура", -492411.360, 2732408.180, 5723060.720},
	{"UFA1", "Уфа", 2074952.140, 3054867.130, 5182970.870},
	{"UFGS", "Уварово", 2913208.100, 2645472.850, 5002867.330},
	{"UNGN", "Уньюган", 1273660.050, 2723541.600, 5606263.360},
	{"USNR", "Усть-Нера", -2200429.460, 1643627.220, 5737741.270},
	{"VANA", "Ванавара", -674522.600, 3091845.280, 5519400.120},
	{"VLDV", "Владивосток", -3119695.420, 3441015.480, 4356693.120},
	{"VLS1", "Вилюйск", -1483497.570, 2408064.550, 5697677.510},
	{"VLS2", "Вилюйск", -1483498.540, 2408067.750, 5697675.850},
	{"VNOV", "В.Новгород(2)", 2852982.930, 1731970.880, 5417045.800},
	{"VOLG", "Волгоград", 3010529.450, 2942854.530, 4775466.890},
	{"VRH1", "Верхоянск", -1677622.960, 1773312.480, 5872889.660},
	{"VRH2", "Верхоянск", -1677622.230, 1773315.090, 5872889.140},
	{"WAGI", "Вагай", 1214897.920, 3169593.620, 5381780.290},
	{"ZHEL", "Железногорск", -858644.480, 3414089.350, 5301256.410},
	{"ZVRG", "Звериноголовское", 1577945.440, 3363833.070, 5166882.910},
}

// ── RGS API клиент ────────────────────────────────────────────────────────────

var httpClient = &http.Client{
	Timeout: 3 * time.Minute,
	Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // сервер cgkipd.ru использует внутренний CA
	},
}

// rgsLogger — глобальный логгер для API-запросов (устанавливается в main).
var rgsLogger *zap.SugaredLogger

// rgsQuery выполняет GET-запрос к API РГС.
// Строит URL без url.Values.Encode(), как Python-клиент РГС (без экранирования).
func rgsQuery(base, method string, params map[string]string) ([]byte, error) {
	// Строим query string в фиксированном порядке: api_token первым, остальные по алфавиту
	query := "api_token=" + params["api_token"]
	keys := make([]string, 0, len(params))
	for k := range params {
		if k != "api_token" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		query += "&" + k + "=" + params[k]
	}
	u := base + "/" + method + "?" + query

	if rgsLogger != nil {
		logURL := strings.ReplaceAll(u, params["api_token"], "***")
		rgsLogger.Infof("RGS GET %s", logURL)
	}

	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return nil, err
	}
	// Имитируем Python urllib User-Agent — некоторые серверы блокируют Go-http-client
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Accept-Language", "ru-RU,ru;q=0.9,en-US;q=0.8,en;q=0.7")
	req.Header.Set("Referer", "https://rgs.cgkipd.ru/")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	// Обрабатываем редиректы вручную (один уровень)
	if resp.StatusCode == 301 || resp.StatusCode == 302 || resp.StatusCode == 307 {
		loc := resp.Header.Get("Location")
		if loc != "" {
			if rgsLogger != nil {
				rgsLogger.Infof("  → редирект на %s", loc)
			}
			req2, _ := http.NewRequest("GET", loc, nil)
			req2.Header.Set("User-Agent", "Python-urllib/3.11")
			req2.Header.Set("Accept", "*/*")
			resp2, err := httpClient.Do(req2)
			if err != nil {
				return nil, err
			}
			defer resp2.Body.Close()
			body, _ = io.ReadAll(resp2.Body)
			if resp2.StatusCode != 200 {
				return nil, fmt.Errorf("HTTP %d после редиректа (%.200s)", resp2.StatusCode, body)
			}
			return body, nil
		}
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d (%.200s)", resp.StatusCode, body)
	}
	return body, nil
}

// rgsQuery2 строит запрос с поддержкой массивных параметров (type[0]=O&type[1]=D).
// Принимает срезы пар ключ-значение в нужном порядке.
func rgsQuery2(base, method string, pairs [][2]string) ([]byte, error) {
	parts := make([]string, 0, len(pairs)+1)
	var token string
	for _, p := range pairs {
		if p[0] == "api_token" {
			token = p[1]
		} else {
			parts = append(parts, p[0]+"="+p[1])
		}
	}
	query := "api_token=" + token
	if len(parts) > 0 {
		query += "&" + strings.Join(parts, "&")
	}
	u := base + "/" + method + "?" + query

	if rgsLogger != nil {
		logURL := strings.ReplaceAll(u, token, "***")
		rgsLogger.Infof("RGS GET %s", logURL)
	}

	req, _ := http.NewRequest("GET", u, nil)
	req.Header.Set("Accept", "application/json, */*")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d (%.300s)", resp.StatusCode, body)
	}
	return body, nil
}

// rgsListFiles возвращает список файлов наблюдений для станции и даты.
// Использует параметры согласно документации API РГС (rgs.cgkipd.ru/rest-api).
func rgsListFiles(token, stationCode string, date time.Time) ([]string, error) {
	year := strconv.Itoa(date.Year())
	day := strconv.Itoa(date.YearDay()) // без ведущих нулей, как в документации
	dateISO := date.Format("2006-01-02")
	upper := strings.ToUpper(stationCode)

	// Параметры согласно документации API:
	//   working_center — 4-х символьный код пункта ФАГС
	//   year           — год YYYY
	//   day            — день года (целое число, не DOY с нулями)
	//   type           — тип файла: O=наблюдения, D=Hatanaka-сжатые наблюдения
	type attempt struct {
		desc  string
		pairs [][2]string
	}
	attempts := []attempt{
		// Без type-фильтра — получить все файлы, потом выбрать нужный
		{
			"working_center+year+day",
			[][2]string{
				{"api_token", token},
				{"working_center", upper},
				{"year", year},
				{"day", day},
			},
		},
		// С date вместо year+day
		{
			"working_center+date",
			[][2]string{
				{"api_token", token},
				{"working_center", upper},
				{"date", dateISO},
			},
		},
		// Lowercase код станции
		{
			"working_center(lower)+year+day",
			[][2]string{
				{"api_token", token},
				{"working_center", strings.ToLower(stationCode)},
				{"year", year},
				{"day", day},
			},
		},
	}

	var lastBody []byte
	for _, a := range attempts {
		data, err := rgsQuery2(rgsAPIBase, "files", a.pairs)
		if err != nil {
			if rgsLogger != nil {
				rgsLogger.Warnf("  [%s] %s: %v", stationCode, a.desc, err)
			}
			continue
		}
		lastBody = data
		names := parseFileNames(data)
		if len(names) > 0 {
			if rgsLogger != nil {
				rgsLogger.Infof("  [%s] %s: найдено %d файлов: %v",
					stationCode, a.desc, len(names), names)
			}
			return names, nil
		}
		if rgsLogger != nil {
			rgsLogger.Warnf("  [%s] %s: пустой список, ответ: %.300s",
				stationCode, a.desc, data)
		}
	}

	hint := ""
	if len(lastBody) > 0 {
		hint = fmt.Sprintf(" (ответ сервера: %.300s)", lastBody)
	}
	return nil, fmt.Errorf("нет файлов для %s на %s%s", stationCode, dateISO, hint)
}

// parseFileNames разбирает ответ API в любом из известных форматов:
//   - []string
//   - [{"name":"..."}, ...]  или "filename"/"file"/"path"
//   - {"data":[...]} / {"files":[...]} / {"result":[...]}
func parseFileNames(data []byte) []string {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return nil
	}

	// Попытка 1: массив строк
	var strs []string
	if err := json.Unmarshal(data, &strs); err == nil && len(strs) > 0 {
		return strs
	}

	// Попытка 2: массив объектов с полем name/filename/file/path/title
	var objs []map[string]json.RawMessage
	if err := json.Unmarshal(data, &objs); err == nil && len(objs) > 0 {
		var names []string
		for _, obj := range objs {
			for _, key := range []string{"name", "filename", "file", "path", "title"} {
				if v, ok := obj[key]; ok {
					var s string
					if json.Unmarshal(v, &s) == nil && s != "" {
						names = append(names, s)
						break
					}
				}
			}
		}
		if len(names) > 0 {
			return names
		}
	}

	// Попытка 3: обёртка {"data":[...]} / {"files":[...]} / {"result":[...]}
	var wrapper map[string]json.RawMessage
	if err := json.Unmarshal(data, &wrapper); err == nil {
		for _, key := range []string{"data", "files", "result", "items", "list"} {
			if raw, ok := wrapper[key]; ok {
				if sub := parseFileNames(raw); len(sub) > 0 {
					return sub
				}
			}
		}
	}

	return nil
}

// pickRINEXFile выбирает файл наблюдений (не навигационный, не метео) из списка.
// RINEX 3 суффиксы типов: MO/GO/RO/EO/CO = наблюдения; GN/RN/EN/CN/MN/IM = навигация; MM = метео.
func pickRINEXFile(names []string) string {
	// Шаг 1: RINEX 3 файлы наблюдений — суффикс _MO/_GO/_RO/_EO/_CO перед расширением
	obsR3Suffixes := []string{"_MO.crx.gz", "_MO.rnx.gz", "_MO.crx", "_MO.rnx",
		"_GO.crx.gz", "_GO.rnx.gz", "_RO.crx.gz", "_RO.rnx.gz"}
	for _, suf := range obsR3Suffixes {
		for _, n := range names {
			if strings.HasSuffix(strings.ToUpper(n), strings.ToUpper(suf)) {
				return n
			}
		}
	}

	// Шаг 2: RINEX 2 файлы наблюдений (.xxo, .xxd, .d.Z, .o.Z)
	r2Suffixes := []string{".d.gz", ".d.Z", ".o.gz", ".o.Z"}
	for _, suf := range r2Suffixes {
		for _, n := range names {
			if strings.HasSuffix(strings.ToLower(n), suf) {
				return n
			}
		}
	}
	// RINEX 2 без сжатия (.XXo, .XXd)
	for _, n := range names {
		base := strings.ToLower(filepath.Base(n))
		if len(base) > 3 {
			ext := base[len(base)-3:]
			if ext[2] == 'o' || ext[2] == 'd' {
				return n
			}
		}
	}

	return ""
}

// rgsDownloadByName скачивает файл по имени через /file/{name}.
func rgsDownloadByName(token, filename, destPath string) error {
	data, err := rgsQuery2(rgsAPIBase, "file/"+filename,
		[][2]string{{"api_token", token}})
	if err != nil {
		return err
	}
	if len(data) < 10 {
		return fmt.Errorf("пустой ответ для %s", filename)
	}
	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		return err
	}
	return os.WriteFile(destPath, data, 0644)
}

// rgsDownloadByFilter скачивает файл через /files/download с теми же фильтрами что и listing.
func rgsDownloadByFilter(token, stationCode string, date time.Time, destPath string) error {
	year := strconv.Itoa(date.Year())
	day := strconv.Itoa(date.YearDay())
	upper := strings.ToUpper(stationCode)
	data, err := rgsQuery2(rgsAPIBase, "files/download", [][2]string{
		{"api_token", token},
		{"working_center", upper},
		{"year", year},
		{"day", day},
	})
	if err != nil {
		return err
	}
	if len(data) < 10 {
		return fmt.Errorf("пустой ответ для %s %s", stationCode, date.Format("2006-01-02"))
	}
	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		return err
	}
	return os.WriteFile(destPath, data, 0644)
}

// downloadFAGSObs скачивает суточный файл наблюдений ФАГС через API РГС.
func downloadFAGSObs(token, stationCode string, date time.Time, taskDir string,
	conv *services.ConverterService) (string, error) {

	// Шаг 1: получаем список файлов
	names, err := rgsListFiles(token, stationCode, date)
	if err != nil {
		// Резервный вариант: скачать напрямую через files/download без предварительного листинга
		rawPath := filepath.Join(taskDir, strings.ToUpper(stationCode)+
			fmt.Sprintf("%03d0.%02dd.gz", date.YearDay(), date.Year()%100))
		if rgsLogger != nil {
			rgsLogger.Infof("  [%s] листинг не удался, пробуем files/download напрямую", stationCode)
		}
		if dlErr := rgsDownloadByFilter(token, stationCode, date, rawPath); dlErr != nil {
			return "", fmt.Errorf("listing: %v; direct download: %v", err, dlErr)
		}
		return finishObs(rawPath, taskDir, stationCode, conv)
	}

	filename := pickRINEXFile(names)
	if filename == "" {
		return "", fmt.Errorf("нет RINEX-файла наблюдений в списке: %v", names)
	}

	if rgsLogger != nil {
		rgsLogger.Infof("  [%s] скачиваем: %s", stationCode, filename)
	}

	rawPath := filepath.Join(taskDir, filepath.Base(filename))
	if err := rgsDownloadByName(token, filename, rawPath); err != nil {
		return "", fmt.Errorf("download %s: %w", filename, err)
	}
	return finishObs(rawPath, taskDir, stationCode, conv)
}

func finishObs(rawPath, taskDir, stationCode string, conv *services.ConverterService) (string, error) {
	info, _ := os.Stat(rawPath)
	if info == nil || info.Size() == 0 {
		return "", fmt.Errorf("скачанный файл пустой: %s", rawPath)
	}
	if rgsLogger != nil {
		rgsLogger.Infof("  [%s] скачано %d байт → %s", stationCode, info.Size(), filepath.Base(rawPath))
	}
	out, err := conv.ConvertFile(rawPath, taskDir)
	if err != nil {
		return "", fmt.Errorf("convert %s: %w", rawPath, err)
	}
	return out, nil
}

// ── эталонные координаты (внешний CSV) ───────────────────────────────────────

// loadRefCoords читает CSV/CSV-like файл с эталонными координатами ITRF2020.
//
// Поддерживаемые форматы (определяется автоматически по заголовку):
//
//	BLH: ID;Latitude(deg);Longitude(deg);Height(m)   — разделитель ; или ,
//	XYZ: code,x_m,y_m,z_m                            — разделитель ; или ,
//
// BLH конвертируется в ECEF XYZ через llhToXYZ.
func loadRefCoords(path string) (map[string][3]float64, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	// Определяем разделитель по первой строке
	firstLine := strings.SplitN(string(raw), "\n", 2)[0]
	delim := ','
	if strings.Count(firstLine, ";") > strings.Count(firstLine, ",") {
		delim = ';'
	}

	r := csv.NewReader(strings.NewReader(string(raw)))
	r.Comma = rune(delim)
	r.TrimLeadingSpace = true

	header, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("заголовок: %w", err)
	}

	// Определяем формат по заголовку: BLH (Latitude/lat) или XYZ (x_m/X)
	isBLH := false
	for _, h := range header {
		hl := strings.ToLower(strings.TrimSpace(h))
		if strings.Contains(hl, "lat") || strings.Contains(hl, "lon") {
			isBLH = true
			break
		}
	}

	m := make(map[string][3]float64)
	for {
		rec, err := r.Read()
		if err != nil {
			break
		}
		if len(rec) < 4 {
			continue
		}
		code := strings.ToUpper(strings.TrimSpace(rec[0]))
		a, ea := strconv.ParseFloat(strings.TrimSpace(rec[1]), 64)
		b, eb := strconv.ParseFloat(strings.TrimSpace(rec[2]), 64)
		c, ec := strconv.ParseFloat(strings.TrimSpace(rec[3]), 64)
		if ea != nil || eb != nil || ec != nil {
			continue
		}
		if isBLH {
			// a=Latitude(deg), b=Longitude(deg), c=Height(m) → XYZ
			x, y, z := llhToXYZ(a, b, c)
			m[code] = [3]float64{x, y, z}
		} else {
			m[code] = [3]float64{a, b, c}
		}
	}
	return m, nil
}

// writeComparisonFile записывает файл сравнения PPP vs эталон в формате IGS.
// Выходной формат совместим с IGS AC residual files (SITE YYYY DOY dN dE dU ...).
func writeComparisonFile(outPath, refPath string, results []StationResult) error {
	f, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer f.Close()

	now := time.Now()
	doy := func(t time.Time) int { return t.YearDay() }

	fmt.Fprintf(f, "%%=FAGSPPP_COMP 1.0 %02d:%03d:%05d fagsbatch\n",
		now.Year()%100, doy(now), int(now.Hour()*3600+now.Minute()*60+now.Second()))
	fmt.Fprintf(f, "+REF_FILE\n %s\n-REF_FILE\n", refPath)
	fmt.Fprintf(f, "+RESIDUALS\n")
	fmt.Fprintf(f, "*%-8s %4s %3s  %15s  %15s  %15s  %10s  %10s  %10s  %9s  Q  NSat\n",
		"SITE", "YYYY", "DOY", "X_PPP(m)", "Y_PPP(m)", "Z_PPP(m)",
		"dN(m)", "dE(m)", "dU(m)", "RMS3D(m)")

	for _, r := range results {
		if r.Status != "ok" {
			continue
		}
		pppX, pppY, pppZ := llhToXYZ(r.Lat, r.Lon, r.H)
		fmt.Fprintf(f, " %-8s %4d %3d  %15.4f  %15.4f  %15.4f  %10.6f  %10.6f  %10.6f  %9.6f  %d  %4d\n",
			r.Code, r.Date.Year(), doy(r.Date),
			pppX, pppY, pppZ,
			r.DN, r.DE, r.DU, r.R3D,
			r.Q, r.NSat)
	}
	fmt.Fprintf(f, "-RESIDUALS\n%%ENDFAGSPPP\n")
	return nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

func ftpCurl(remotePath, destPath string) error {
	u := "ftp://gdc.cddis.eosdis.nasa.gov:21" + remotePath
	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		return err
	}
	cmd := exec.Command("curl", "--ssl-reqd", "-u", "anonymous:anonymous",
		"--silent", "--show-error", "--max-time", "120", "--output", destPath, u)
	out, err := cmd.CombinedOutput()
	if err != nil {
		os.Remove(destPath)
		return fmt.Errorf("ftp %s: %s", remotePath, strings.TrimSpace(string(out)))
	}
	info, _ := os.Stat(destPath)
	if info == nil || info.Size() == 0 {
		os.Remove(destPath)
		return fmt.Errorf("empty: %s", remotePath)
	}
	return nil
}

func httpGetFile(u, destPath string) error {
	resp, err := httpClient.Get(u)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, u)
	}
	os.MkdirAll(filepath.Dir(destPath), 0755)
	f, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer f.Close()
	n, err := io.Copy(f, resp.Body)
	if err != nil {
		return err
	}
	if n == 0 {
		os.Remove(destPath)
		return fmt.Errorf("empty: %s", u)
	}
	return nil
}

func extractAntenna(rinexPath string) (antType string, dH, dE, dN float64) {
	antType = "NONE"
	f, err := os.Open(rinexPath)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if strings.Contains(line, "END OF HEADER") {
			break
		}
		if strings.Contains(line, "ANT # / TYPE") && len(line) >= 60 {
			t := strings.TrimSpace(line[20:60])
			if t != "" {
				antType = t
			}
		}
		if strings.Contains(line, "ANTENNA: DELTA H/E/N") && len(line) >= 42 {
			fmt.Sscanf(strings.TrimSpace(line[:14]), "%f", &dH)
			fmt.Sscanf(strings.TrimSpace(line[14:28]), "%f", &dE)
			fmt.Sscanf(strings.TrimSpace(line[28:42]), "%f", &dN)
		}
	}
	return
}

func overrideLine(content, prefix, newLine string) string {
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimLeft(line, " "), prefix) {
			lines[i] = newLine
			return strings.Join(lines, "\n")
		}
	}
	return content
}

func writePPPConfig(taskDir, taskID, antType string, antH, antE, antN,
	posX, posY, posZ float64, biaFile, erpFile, blqFile string) (string, error) {

	raw, err := os.ReadFile(pppConfFile)
	if err != nil {
		return "", fmt.Errorf("read ppp.conf: %w", err)
	}
	var stripped strings.Builder
	for _, line := range strings.Split(string(raw), "\n") {
		if idx := strings.Index(line, " #"); idx >= 0 {
			line = strings.TrimRight(line[:idx], " \t")
		}
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		stripped.WriteString(line)
		stripped.WriteByte('\n')
	}
	r := strings.NewReplacer(
		"{{POS_MODE}}", "ppp-static",
		"{{SNR_MASK_R}}", "off", "{{SNR_MASK_B}}", "off",
		"{{SNR_MASK_L1}}", "", "{{SNR_MASK_L2}}", "", "{{SNR_MASK_L5}}", "",
		"{{ANT_TYPE}}", antType,
		"{{ANT_DELTA_U}}", strconv.FormatFloat(antH, 'f', 4, 64),
		"{{ANT_DELTA_E}}", strconv.FormatFloat(antE, 'f', 4, 64),
		"{{ANT_DELTA_N}}", strconv.FormatFloat(antN, 'f', 4, 64),
		"{{BIA_FILE}}", biaFile,
		"{{DCB_FILE}}", "", // пусто — иначе SIGTRAP с osb_cod
		serviceRoot+"/{{ERP_FILE}}", erpFile,
		serviceRoot+"/{{BLQ_FILE}}", blqFile,
	)
	content := r.Replace(stripped.String())
	content = overrideLine(content, "ant1-pos1", fmt.Sprintf("ant1-pos1          =%.4f", posX))
	content = overrideLine(content, "ant1-pos2", fmt.Sprintf("ant1-pos2          =%.4f", posY))
	content = overrideLine(content, "ant1-pos3", fmt.Sprintf("ant1-pos3          =%.4f", posZ))
	content = overrideLine(content, "file-tempdir", "file-tempdir       ="+taskDir)
	if blqFile == "" {
		content = overrideLine(content, "pos1-tidecorr", "pos1-tidecorr      =on")
	}
	content = overrideLine(content, "file-solstatfile", "file-solstatfile   =")
	content = overrideLine(content, "file-tracefile", "file-tracefile     =")
	configPath := filepath.Join(taskDir, taskID+"_ppp.conf")
	return configPath, os.WriteFile(configPath, []byte(content), 0644)
}

func parsePPPOutput(posFile string) (lat, lon, h float64, q, nsat int, ok bool) {
	data, err := os.ReadFile(posFile)
	if err != nil {
		return
	}
	type sol struct {
		lat, lon, h float64
		q, nsat     int
	}
	var solutions []sol
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '%' || line[0] == '#' {
			continue
		}
		f := strings.Fields(line)
		if len(f) < 7 {
			continue
		}
		var s sol
		fmt.Sscanf(f[2], "%f", &s.lat)
		fmt.Sscanf(f[3], "%f", &s.lon)
		fmt.Sscanf(f[4], "%f", &s.h)
		fmt.Sscanf(f[5], "%d", &s.q)
		fmt.Sscanf(f[6], "%d", &s.nsat)
		solutions = append(solutions, s)
	}
	if len(solutions) == 0 {
		return
	}
	for i := len(solutions) - 1; i >= 0; i-- {
		if solutions[i].q == 1 {
			s := solutions[i]
			return s.lat, s.lon, s.h, s.q, s.nsat, true
		}
	}
	for i := len(solutions) - 1; i >= 0; i-- {
		if solutions[i].q == 6 {
			s := solutions[i]
			return s.lat, s.lon, s.h, s.q, s.nsat, true
		}
	}
	s := solutions[len(solutions)-1]
	return s.lat, s.lon, s.h, s.q, s.nsat, true
}

// ── геодезия ──────────────────────────────────────────────────────────────────

const (
	wgs84A  = 6378137.0
	wgs84F  = 1.0 / 298.257223563
	wgs84E2 = 2*wgs84F - wgs84F*wgs84F
)

func xyzToLLH(x, y, z float64) (latDeg, lonDeg, h float64) {
	lonDeg = math.Atan2(y, x) * 180 / math.Pi
	p := math.Sqrt(x*x + y*y)
	lat := math.Atan2(z, p*(1-wgs84E2))
	for i := 0; i < 10; i++ {
		sinLat := math.Sin(lat)
		N := wgs84A / math.Sqrt(1-wgs84E2*sinLat*sinLat)
		next := math.Atan2(z+wgs84E2*N*sinLat, p)
		if math.Abs(next-lat) < 1e-12 {
			lat = next
			break
		}
		lat = next
	}
	sinLat := math.Sin(lat)
	N := wgs84A / math.Sqrt(1-wgs84E2*sinLat*sinLat)
	h = p/math.Cos(lat) - N
	latDeg = lat * 180 / math.Pi
	return
}

func llhToXYZ(latDeg, lonDeg, h float64) (x, y, z float64) {
	latR := latDeg * math.Pi / 180
	lonR := lonDeg * math.Pi / 180
	sinLat, cosLat := math.Sin(latR), math.Cos(latR)
	N := wgs84A / math.Sqrt(1-wgs84E2*sinLat*sinLat)
	x = (N + h) * cosLat * math.Cos(lonR)
	y = (N + h) * cosLat * math.Sin(lonR)
	z = (N*(1-wgs84E2) + h) * sinLat
	return
}

func xyzDiffToNEU(refLatDeg, refLonDeg, dx, dy, dz float64) (dN, dE, dU float64) {
	latR := refLatDeg * math.Pi / 180
	lonR := refLonDeg * math.Pi / 180
	sinLat, cosLat := math.Sin(latR), math.Cos(latR)
	sinLon, cosLon := math.Sin(lonR), math.Cos(lonR)
	dN = -sinLat*cosLon*dx - sinLat*sinLon*dy + cosLat*dz
	dE = -sinLon*dx + cosLon*dy
	dU = cosLat*cosLon*dx + cosLat*sinLon*dy + sinLat*dz
	return
}

// ── продукты (скачаны заранее через fagsprep) ──────────────────────────────────

type DailyProducts struct {
	SP3, CLK, ERP, DCB, BIA, BRDC string
}

func fileReady(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Size() > 0
}

// loadDailyProducts загружает пути к уже скачанным продуктам (fagsprep).
// Возвращает ошибку, если SP3 или CLK отсутствуют.
func loadDailyProducts(workDir string, date time.Time, logger *zap.SugaredLogger) (DailyProducts, error) {
	taskID := "daily_" + date.Format("20060102")
	dir := filepath.Join(workDir, taskID)
	var p DailyProducts

	candidates := []struct {
		field *string
		name  string
		paths []string // имена файлов в порядке приоритета
	}{
		{&p.SP3, "SP3", []string{taskID + "_sp3.sp3"}},
		{&p.CLK, "CLK", []string{taskID + "_clk.clk"}},
		{&p.ERP, "ERP", []string{taskID + "_erp.erp"}},
		{&p.BIA, "BIA", []string{taskID + "_bia.bia"}},
		{&p.DCB, "DCB", []string{taskID + "_dcb.bsx", taskID + "_dcb.bia"}},
		{&p.BRDC, "BRDC", []string{taskID + "_brdc.rnx"}},
	}

	for _, c := range candidates {
		for _, fname := range c.paths {
			full := filepath.Join(dir, fname)
			if fileReady(full) {
				*c.field = full
				break
			}
		}
	}

	// Отчёт
	mark := ""
	for _, c := range candidates {
		if *c.field != "" {
			mark += c.name + "=✓ "
		} else {
			mark += c.name + "=✗ "
		}
	}
	logger.Infof("  %s: %s", date.Format("2006-01-02"), strings.TrimSpace(mark))

	if p.SP3 == "" {
		return p, fmt.Errorf("SP3 не найден в %s — запустите fagsprep", dir)
	}
	if p.CLK == "" {
		return p, fmt.Errorf("CLK не найден в %s — запустите fagsprep", dir)
	}
	return p, nil
}

// ── результат и обработка ─────────────────────────────────────────────────────

type StationResult struct {
	Date                   time.Time
	Code                   string
	City                   string
	Lat, Lon, H            float64 // PPP результат (ITRF2020)
	RefLat, RefLon, RefH   float64 // эталон (ГСК-2011 → ITRF2020)
	Q, NSat                int
	DN, DE, DU             float64
	R3D                    float64
	Status                 string
	DurationSec            float64
}

func processStation(
	st FAGSStation, date time.Time, prods DailyProducts,
	rgsToken string,
	rtk *services.RTKService, conv *services.ConverterService,
	refCoords map[string][3]float64, // ITRF2020 XYZ эталон (из --ref-csv), может быть nil
	logger *zap.SugaredLogger, workDir string, keepTmp bool,
) StationResult {
	start := time.Now()
	res := StationResult{Date: date, Code: st.Code, City: st.City}

	taskID := fmt.Sprintf("fags_%s_%s", st.Code, date.Format("20060102"))
	taskDir := filepath.Join(workDir, taskID)
	if err := os.MkdirAll(taskDir, 0755); err != nil {
		res.Status = "setup_failed"
		return res
	}
	if !keepTmp {
		defer os.RemoveAll(taskDir)
	}

	// Начальное положение для PPP: эталон ITRF2020 (если есть) или каталог ГСК-2011 (~7 см разница — несущественно).
	var initX, initY, initZ float64
	if ref, ok := refCoords[st.Code]; ok {
		initX, initY, initZ = ref[0], ref[1], ref[2]
		res.RefLat, res.RefLon, res.RefH = xyzToLLH(initX, initY, initZ)
	} else {
		initX, initY, initZ = st.X, st.Y, st.Z
		res.RefLat, res.RefLon, res.RefH = xyzToLLH(initX, initY, initZ)
	}

	obsPath, err := downloadFAGSObs(rgsToken, st.Code, date, taskDir, conv)
	if err != nil {
		logger.Warnf("[%s] нет данных: %v", taskID, err)
		res.Status = "no_obs"
		res.DurationSec = time.Since(start).Seconds()
		return res
	}

	antType, antH, antE, antN := extractAntenna(obsPath)

	configPath, err := writePPPConfig(taskDir, taskID, antType, antH, antE, antN,
		initX, initY, initZ, prods.BIA, prods.ERP, "")
	if err != nil {
		res.Status = "config_failed"
		res.DurationSec = time.Since(start).Seconds()
		return res
	}

	posFile, err := rtk.ProcessPPP(obsPath, prods.BRDC, prods.SP3, prods.CLK, configPath, taskID)
	if err != nil {
		logger.Warnf("[%s] PPP ошибка: %v", taskID, err)
		res.Status = "ppp_failed"
		res.DurationSec = time.Since(start).Seconds()
		return res
	}

	lat, lon, h, q, nsat, ok := parsePPPOutput(posFile)
	if !ok {
		res.Status = "parse_failed"
		res.DurationSec = time.Since(start).Seconds()
		return res
	}
	res.Lat, res.Lon, res.H = lat, lon, h
	res.Q, res.NSat = q, nsat
	res.Status = "ok"

	// Сравниваем с эталоном ITRF2020 (только если загружен ref-csv).
	if ref, ok := refCoords[st.Code]; ok {
		pppX, pppY, pppZ := llhToXYZ(lat, lon, h)
		dX := pppX - ref[0]
		dY := pppY - ref[1]
		dZ := pppZ - ref[2]
		res.DN, res.DE, res.DU = xyzDiffToNEU(res.RefLat, res.RefLon, dX, dY, dZ)
		res.R3D = math.Sqrt(dX*dX + dY*dY + dZ*dZ)
		logger.Infof("[%s] PPP: B=%.8f° L=%.8f° H=%.4fm  dN=%.4fm dE=%.4fm dU=%.4fm",
			st.Code, lat, lon, h, res.DN, res.DE, res.DU)
	} else {
		logger.Infof("[%s] PPP: B=%.8f° L=%.8f° H=%.4fm  (эталон не загружен)",
			st.Code, lat, lon, h)
	}

	res.DurationSec = time.Since(start).Seconds()
	return res
}

// ── статистика ────────────────────────────────────────────────────────────────

func rms(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	s := 0.0
	for _, v := range vals {
		s += v * v
	}
	return math.Sqrt(s / float64(len(vals)))
}

type PeriodStats struct {
	Label                       string
	N                           int
	FixPct                      float64
	RMS_N, RMS_E, RMS_U, RMS_3D float64
}

type DetailedStats struct {
	Days, Months, Stations []PeriodStats
	Overall                PeriodStats
}

func computeDetailedStats(csvPath string) DetailedStats {
	f, err := os.Open(csvPath)
	if err != nil {
		return DetailedStats{}
	}
	defer f.Close()

	type row struct {
		date, station, city, month string
		dN, dE, dU, r3d            float64
		q                          int
	}
	r := csv.NewReader(f)
	r.Read()
	var rows []row
	for {
		rec, err := r.Read()
		if err != nil {
			break
		}
		if len(rec) < 13 || rec[12] != "ok" {
			continue
		}
		d := rec[0]
		month := ""
		if len(d) >= 7 {
			month = d[:7]
		}
		dN, _ := strconv.ParseFloat(rec[8], 64)
		dE, _ := strconv.ParseFloat(rec[9], 64)
		dU, _ := strconv.ParseFloat(rec[10], 64)
		r3d, _ := strconv.ParseFloat(rec[11], 64)
		q, _ := strconv.Atoi(rec[6])
		rows = append(rows, row{date: d, station: rec[1], city: rec[2], month: month,
			dN: dN, dE: dE, dU: dU, r3d: r3d, q: q})
	}

	aggregate := func(rs []row, label string) PeriodStats {
		ps := PeriodStats{Label: label, N: len(rs)}
		if len(rs) == 0 {
			return ps
		}
		var ns, es, us, r3s []float64
		fix := 0
		for _, rw := range rs {
			ns = append(ns, rw.dN)
			es = append(es, rw.dE)
			us = append(us, rw.dU)
			r3s = append(r3s, rw.r3d)
			if rw.q == 1 {
				fix++
			}
		}
		ps.RMS_N = rms(ns)
		ps.RMS_E = rms(es)
		ps.RMS_U = rms(us)
		ps.RMS_3D = rms(r3s)
		ps.FixPct = float64(fix) / float64(len(rs)) * 100
		return ps
	}

	byDate := make(map[string][]row)
	byMonth := make(map[string][]row)
	byStation := make(map[string][]row)
	for _, rw := range rows {
		byDate[rw.date] = append(byDate[rw.date], rw)
		byMonth[rw.month] = append(byMonth[rw.month], rw)
		key := rw.station + " " + rw.city
		byStation[key] = append(byStation[key], rw)
	}

	keys := func(m map[string][]row) []string {
		ks := make([]string, 0, len(m))
		for k := range m {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		return ks
	}

	var days, months, stations []PeriodStats
	for _, d := range keys(byDate) {
		days = append(days, aggregate(byDate[d], d))
	}
	for _, m := range keys(byMonth) {
		months = append(months, aggregate(byMonth[m], m))
	}
	for _, s := range keys(byStation) {
		stations = append(stations, aggregate(byStation[s], s))
	}
	sort.Slice(stations, func(i, j int) bool { return stations[i].RMS_3D < stations[j].RMS_3D })

	return DetailedStats{
		Days: days, Months: months, Stations: stations,
		Overall: aggregate(rows, "Overall"),
	}
}

// ── HTML отчёт ────────────────────────────────────────────────────────────────

const htmlTpl = `<!DOCTYPE html>
<html><head><meta charset="utf-8">
<title>ФАГС PPP-AR Отчёт</title>
<style>
body{font-family:sans-serif;margin:2em;color:#222}
h1{border-bottom:2px solid #336;padding-bottom:.3em}
h2{color:#336;margin-top:2em}
table{border-collapse:collapse;width:100%;margin-bottom:1.5em}
th,td{border:1px solid #ccc;padding:5px 10px;text-align:right;font-size:.88em}
th{background:#e8eaf0;text-align:center}
td.label{text-align:left;font-family:monospace;font-weight:bold}
tr.total{font-weight:bold;background:#d8e8ff}
tr.good{background:#f0fff4}
tr.warn{background:#fffde7}
tr.bad{background:#fff0f0}
.summary{background:#f5f5ff;border:1px solid #aac;padding:1em 1.5em;
  border-radius:6px;margin-bottom:1.5em;display:flex;flex-wrap:wrap;gap:1.2em}
.kv{display:flex;flex-direction:column;align-items:center}
.kv .val{font-size:1.4em;font-weight:bold;color:#336}
.kv .key{font-size:.75em;color:#666}
.note{font-size:.82em;color:#888;font-style:italic}
</style></head><body>
<h1>ФАГС PPP-AR Batch Report</h1>
<p style="color:#666">Сгенерирован: {{.Generated}}</p>
<p class="note">PPP-результат (ITRF2020) преобразован в ГСК-2011 через geocentric.xyz и сравнён с каталогом ГСК-2011.
Продукты CODE (SP3/CLK). Сравнение в единой системе ГСК-2011.</p>

<div class="summary">
  <div class="kv"><span class="val">{{.Overall.N}}</span><span class="key">Решений (ok)</span></div>
  <div class="kv"><span class="val">{{printf "%.1f" .Overall.FixPct}}%</span><span class="key">Fix (Q=1)</span></div>
  <div class="kv"><span class="val">{{printf "%.2f" (cm .Overall.RMS_N)}}</span><span class="key">RMS N (cm)</span></div>
  <div class="kv"><span class="val">{{printf "%.2f" (cm .Overall.RMS_E)}}</span><span class="key">RMS E (cm)</span></div>
  <div class="kv"><span class="val">{{printf "%.2f" (cm .Overall.RMS_U)}}</span><span class="key">RMS U (cm)</span></div>
  <div class="kv"><span class="val">{{printf "%.2f" (cm .Overall.RMS_3D)}}</span><span class="key">RMS 3D (cm)</span></div>
</div>

<h2>Точность по дням</h2>
<table>
<tr><th>Дата</th><th>N</th><th>Fix%</th><th>RMS N (cm)</th><th>RMS E (cm)</th><th>RMS U (cm)</th><th>RMS 3D (cm)</th></tr>
{{range .Days}}<tr class="{{rowclass .RMS_3D}}">
  <td class="label">{{.Label}}</td><td>{{.N}}</td><td>{{printf "%.1f" .FixPct}}</td>
  <td>{{printf "%.2f" (cm .RMS_N)}}</td><td>{{printf "%.2f" (cm .RMS_E)}}</td>
  <td>{{printf "%.2f" (cm .RMS_U)}}</td><td>{{printf "%.2f" (cm .RMS_3D)}}</td>
</tr>{{end}}
<tr class="total">
  <td class="label">Итого</td><td>{{.Overall.N}}</td><td>{{printf "%.1f" .Overall.FixPct}}</td>
  <td>{{printf "%.2f" (cm .Overall.RMS_N)}}</td><td>{{printf "%.2f" (cm .Overall.RMS_E)}}</td>
  <td>{{printf "%.2f" (cm .Overall.RMS_U)}}</td><td>{{printf "%.2f" (cm .Overall.RMS_3D)}}</td>
</tr></table>

{{if gt (len .Months) 1}}
<h2>Кумулятивная точность по месяцам</h2>
<table>
<tr><th>Месяц</th><th>N</th><th>Fix%</th><th>RMS N (cm)</th><th>RMS E (cm)</th><th>RMS U (cm)</th><th>RMS 3D (cm)</th></tr>
{{range .Months}}<tr class="{{rowclass .RMS_3D}}">
  <td class="label">{{.Label}}</td><td>{{.N}}</td><td>{{printf "%.1f" .FixPct}}</td>
  <td>{{printf "%.2f" (cm .RMS_N)}}</td><td>{{printf "%.2f" (cm .RMS_E)}}</td>
  <td>{{printf "%.2f" (cm .RMS_U)}}</td><td>{{printf "%.2f" (cm .RMS_3D)}}</td>
</tr>{{end}}
</table>{{end}}

<h2>Точность по пунктам ФАГС (сортировка по RMS 3D ↑)</h2>
<table>
<tr><th>Пункт / Город</th><th>N дней</th><th>Fix%</th><th>RMS N (cm)</th><th>RMS E (cm)</th><th>RMS U (cm)</th><th>RMS 3D (cm)</th></tr>
{{range .Stations}}<tr class="{{rowclass .RMS_3D}}">
  <td class="label">{{.Label}}</td><td>{{.N}}</td><td>{{printf "%.1f" .FixPct}}</td>
  <td>{{printf "%.2f" (cm .RMS_N)}}</td><td>{{printf "%.2f" (cm .RMS_E)}}</td>
  <td>{{printf "%.2f" (cm .RMS_U)}}</td><td>{{printf "%.2f" (cm .RMS_3D)}}</td>
</tr>{{end}}
</table>
</body></html>`

func writeHTMLReport(outPath, csvPath string) error {
	stats := computeDetailedStats(csvPath)
	funcMap := template.FuncMap{
		"cm": func(m float64) float64 { return m * 100 },
		"rowclass": func(r3d float64) string {
			switch {
			case r3d*100 < 2.0:
				return "good"
			case r3d*100 < 5.0:
				return "warn"
			default:
				return "bad"
			}
		},
	}
	tmpl, err := template.New("r").Funcs(funcMap).Parse(htmlTpl)
	if err != nil {
		return err
	}
	f, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer f.Close()
	return tmpl.Execute(f, map[string]interface{}{
		"Generated": time.Now().Format("2006-01-02 15:04:05"),
		"Overall":   stats.Overall,
		"Days":      stats.Days,
		"Months":    stats.Months,
		"Stations":  stats.Stations,
	})
}

// ── probe mode ───────────────────────────────────────────────────────────────

// probeAPI прощупывает сервер РГС, пытаясь найти рабочий базовый URL и эндпоинт.
func probeAPI(token, stationCode string) {
	fmt.Println("\n═══ Зондирование API РГС ═══")
	fmt.Printf("Сервер: rgs-centre.ru  Пункт: %s  Токен: %s...\n\n",
		stationCode, token[:8])

	doGet := func(u string) (int, string) {
		req, _ := http.NewRequest("GET", u, nil)
		req.Header.Set("User-Agent", "Python-urllib/3.11")
		req.Header.Set("Accept", "application/json, */*")
		resp, err := httpClient.Do(req)
		if err != nil {
			return 0, "ERR: " + err.Error()
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 600))
		s := strings.TrimSpace(string(body))
		isHTML := strings.HasPrefix(s, "<!") || strings.HasPrefix(s, "<html") || strings.HasPrefix(s, "<H")
		if isHTML {
			// Вырезаем title
			title := ""
			if i := strings.Index(s, "<title>"); i >= 0 {
				e := strings.Index(s[i:], "</title>")
				if e >= 0 {
					title = s[i+7 : i+e]
				}
			}
			return resp.StatusCode, "[HTML] " + title
		}
		if len(s) > 200 {
			s = s[:200] + "…"
		}
		return resp.StatusCode, s
	}

	show := func(label, u string) {
		display := strings.ReplaceAll(u, token, "***TOKEN***")
		code, body := doGet(u)
		mark := "✗"
		if code == 200 {
			mark = "✓"
		}
		fmt.Printf("  %s HTTP %-3d  %-55s  %s\n", mark, code, display, body)
	}

	lower := strings.ToLower(stationCode)
	t := token

	fmt.Println("── Корень сервера ──")
	show("root https", "https://rgs-centre.ru/")
	show("root http ", "http://rgs-centre.ru/")

	fmt.Println("\n── Пути /api/* ──")
	for _, base := range []string{"https://rgs-centre.ru", "http://rgs-centre.ru"} {
		show(base+"/api", base+"/api?api_token="+t)
		show(base+"/api/", base+"/api/?api_token="+t)
		show(base+"/api/files", base+"/api/files?api_token="+t)
		show(base+"/api/files/", base+"/api/files/?api_token="+t)
		show(base+"/api/fags", base+"/api/fags?api_token="+t)
		show(base+"/api/fags/"+lower, base+"/api/fags/"+lower+"?api_token="+t)
	}

	fmt.Println("\n── Другие пути ──")
	for _, path := range []string{"/rinex/", "/data/", "/gnss/", "/catalog/", "/archive/", "/obs/"} {
		show("https://rgs-centre.ru"+path, "https://rgs-centre.ru"+path)
	}

	fmt.Println("\n── С параметрами станции ──")
	variants := []string{
		"https://rgs-centre.ru/api/files?api_token=" + t + "&station=" + lower + "&year=2025&doy=001",
		"https://rgs-centre.ru/api/files?api_token=" + t + "&station=" + lower + "&year=2025&doy=1",
		"https://rgs-centre.ru/api/files?api_token=" + t + "&station=" + strings.ToUpper(stationCode) + "&year=2025&doy=001",
		"https://rgs-centre.ru/api/rinex?api_token=" + t + "&station=" + lower + "&year=2025&doy=001",
		"https://rgs-centre.ru/api/data?api_token=" + t + "&station=" + lower + "&year=2025&doy=001",
	}
	for _, u := range variants {
		show("", u)
	}

	fmt.Println("\n═══ Результат ═══")
	fmt.Println("✓ = HTTP 200, ✗ = другой код")
	fmt.Println("Если все 404 — VPN не имеет доступа к публичному rgs-centre.ru,")
	fmt.Println("  возможно нужен внутренний IP-адрес. Попросите у владельцев API правильный URL.")
	fmt.Println("Если нашёлся рабочий URL — используйте: ./fagsbatch --api-base <URL>")
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ── вспомогательное ───────────────────────────────────────────────────────────

func buildDates(start, end time.Time, step int) []time.Time {
	var dates []time.Time
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		if (d.YearDay()-1)%step == 0 {
			dates = append(dates, d)
		}
	}
	return dates
}

// recomputeDeltas пересчитывает dN/dE/dU в существующем CSV используя ref-csv.
// Обновляет все строки status=ok с ненулевыми PPP-координатами, для которых есть эталон.
// Возвращает количество обновлённых строк.
func recomputeDeltas(csvPath, refCSVPath string) (int, error) {
	refCoords, err := loadRefCoords(refCSVPath)
	if err != nil {
		return 0, fmt.Errorf("ref-csv: %w", err)
	}

	f, err := os.Open(csvPath)
	if err != nil {
		return 0, err
	}
	r := csv.NewReader(f)
	header, err := r.Read()
	if err != nil {
		f.Close()
		return 0, err
	}
	var rows [][]string
	for {
		rec, err := r.Read()
		if err != nil {
			break
		}
		rows = append(rows, rec)
	}
	f.Close()

	// CSV columns: date(0) station(1) city(2) lat(3) lon(4) h_m(5) q(6) nsat(7)
	//              dN_m(8) dE_m(9) dU_m(10) r3d_m(11) status(12) duration_s(13)
	updated := 0
	for i, rec := range rows {
		if len(rec) < 13 || rec[12] != "ok" {
			continue
		}
		lat, _ := strconv.ParseFloat(rec[3], 64)
		lon, _ := strconv.ParseFloat(rec[4], 64)
		h, _ := strconv.ParseFloat(rec[5], 64)
		if lat == 0 && lon == 0 {
			continue
		}
		code := strings.ToUpper(rec[1])
		ref, ok := refCoords[code]
		if !ok {
			continue
		}
		pppX, pppY, pppZ := llhToXYZ(lat, lon, h)
		refLat, refLon, _ := xyzToLLH(ref[0], ref[1], ref[2])
		dX, dY, dZ := pppX-ref[0], pppY-ref[1], pppZ-ref[2]
		dN, dE, dU := xyzDiffToNEU(refLat, refLon, dX, dY, dZ)
		r3d := math.Sqrt(dX*dX + dY*dY + dZ*dZ)

		rows[i][8] = strconv.FormatFloat(dN, 'f', 4, 64)
		rows[i][9] = strconv.FormatFloat(dE, 'f', 4, 64)
		rows[i][10] = strconv.FormatFloat(dU, 'f', 4, 64)
		rows[i][11] = strconv.FormatFloat(r3d, 'f', 4, 64)
		updated++
	}

	// Перезаписываем CSV
	fw, err := os.Create(csvPath)
	if err != nil {
		return 0, err
	}
	w := csv.NewWriter(fw)
	w.Write(header)
	w.WriteAll(rows)
	w.Flush()
	fw.Close()
	return updated, w.Error()
}

func loadDone(csvPath string) map[string]bool {
	done := make(map[string]bool)
	f, err := os.Open(csvPath)
	if err != nil {
		return done
	}
	defer f.Close()
	r := csv.NewReader(f)
	r.Read()
	for {
		rec, err := r.Read()
		if err != nil {
			break
		}
		if len(rec) >= 2 {
			done[rec[0]+"_"+rec[1]] = true
		}
	}
	return done
}

// ── main ──────────────────────────────────────────────────────────────────────

func main() {
	startDateStr := flag.String("start-date", "2025-01-01", "Начало периода YYYY-MM-DD")
	endDateStr := flag.String("end-date", "2025-01-31", "Конец периода YYYY-MM-DD")
	sampleStep := flag.Int("sample-step", 1, "Шаг выборки (1 = каждый день)")
	workers := flag.Int("workers", 4, "Параллельных воркеров на день")
	defaultWorkDir := os.Getenv("HOME") + "/fagsbatch_data"
	workDir := flag.String("work-dir", defaultWorkDir, "Рабочая директория (та же, что у fagsprep)")
	outCSV := flag.String("out-csv", "fags_batch_report.csv", "Выходной CSV")
	outHTML := flag.String("out-html", "fags_batch_report.html", "Выходной HTML")
	rgsToken := flag.String("token", "3E3hpgxcIOc6EUfhyEiY4ATvi8arwv64ZUwl4DNkvMMMPwzhQxhl1xARtnt5",
		"API token rgs-centre.ru")
	apiBase := flag.String("api-base", rgsAPIBaseHTTPS,
		"Базовый URL API РГС (напр. http://rgs-centre.ru/api)")
	stationFilter := flag.String("station", "", "Обработать только один пункт (4-симв. код, для теста)")
	probeMode := flag.Bool("probe", false, "Зондировать API РГС для поиска рабочего URL (используй с --station)")
	resume := flag.Bool("resume", false, "Пропустить уже обработанные")
	keepTmp := flag.Bool("keep-tmp", false, "Оставить временные папки")
	refCSV := flag.String("ref-csv", "", "CSV с эталонными координатами ITRF2020 (code,x_m,y_m,z_m)")
	outComp := flag.String("out-comp", "fags_comparison.txt", "Файл сравнения в формате IGS")
	recompute := flag.Bool("recompute", false, "Пересчитать dN/dE/dU в уже готовом --out-csv (требует --ref-csv)")
	flag.Parse()

	zapCfg := zap.NewDevelopmentConfig()
	zapCfg.Level = zap.NewAtomicLevelAt(zap.InfoLevel)
	logger, _ := zapCfg.Build()
	sugar := logger.Sugar()
	zap.ReplaceGlobals(logger)
	defer logger.Sync()

	// Инициализируем базовый URL и логгер для API-запросов РГС
	rgsAPIBase = *apiBase
	rgsLogger = sugar
	sugar.Infof("API РГС: %s", rgsAPIBase)

	// Режим пересчёта дельт в готовом CSV
	if *recompute {
		if *refCSV == "" {
			sugar.Fatal("--recompute требует --ref-csv")
		}
		n, err := recomputeDeltas(*outCSV, *refCSV)
		if err != nil {
			sugar.Fatalf("recompute: %v", err)
		}
		sugar.Infof("Обновлено строк: %d → %s", n, *outCSV)
		if err := writeHTMLReport(*outHTML, *outCSV); err != nil {
			sugar.Warnf("HTML: %v", err)
		} else {
			sugar.Infof("Отчёт: %s", *outHTML)
		}
		// Собираем результаты для файла сравнения
		var compResults []StationResult
		f, _ := os.Open(*outCSV)
		if f != nil {
			cr := csv.NewReader(f)
			cr.Read()
			for {
				rec, err := cr.Read()
				if err != nil || len(rec) < 14 {
					break
				}
				if rec[12] != "ok" {
					continue
				}
				date, _ := time.Parse("2006-01-02", rec[0])
				lat, _ := strconv.ParseFloat(rec[3], 64)
				lon, _ := strconv.ParseFloat(rec[4], 64)
				h, _ := strconv.ParseFloat(rec[5], 64)
				q, _ := strconv.Atoi(rec[6])
				nsat, _ := strconv.Atoi(rec[7])
				dN, _ := strconv.ParseFloat(rec[8], 64)
				dE, _ := strconv.ParseFloat(rec[9], 64)
				dU, _ := strconv.ParseFloat(rec[10], 64)
				r3d, _ := strconv.ParseFloat(rec[11], 64)
				compResults = append(compResults, StationResult{
					Date: date, Code: rec[1], City: rec[2],
					Lat: lat, Lon: lon, H: h,
					Q: q, NSat: nsat,
					DN: dN, DE: dE, DU: dU, R3D: r3d,
					Status: "ok",
				})
			}
			f.Close()
		}
		if err := writeComparisonFile(*outComp, *refCSV, compResults); err != nil {
			sugar.Warnf("Файл сравнения: %v", err)
		} else {
			sugar.Infof("Сравнение: %s", *outComp)
		}
		return
	}

	// Режим зондирования API
	if *probeMode {
		code := "AMDR"
		if *stationFilter != "" {
			code = strings.ToUpper(*stationFilter)
		}
		probeAPI(*rgsToken, code)
		return
	}

	startDate, _ := time.Parse("2006-01-02", *startDateStr)
	endDate, _ := time.Parse("2006-01-02", *endDateStr)
	if startDate.IsZero() || endDate.IsZero() {
		sugar.Fatal("Неверный формат дат")
	}

	// Фильтр станций
	catalog := fagsCatalog
	if *stationFilter != "" {
		code := strings.ToUpper(*stationFilter)
		var filtered []FAGSStation
		for _, s := range catalog {
			if s.Code == code {
				filtered = append(filtered, s)
			}
		}
		if len(filtered) == 0 {
			sugar.Fatalf("Пункт %s не найден в каталоге ФАГС", code)
		}
		catalog = filtered
		sugar.Infof("Режим тестирования: только пункт %s", code)
	}

	sugar.Infof("Пунктов ФАГС в каталоге: %d", len(catalog))
	sugar.Infof("Период: %s – %s", startDate.Format("2006-01-02"), endDate.Format("2006-01-02"))

	dates := buildDates(startDate, endDate, *sampleStep)
	total := len(dates) * len(catalog)
	sugar.Infof("Задач: %d дней × %d пунктов = %d", len(dates), len(catalog), total)

	if err := os.MkdirAll(*workDir, 0755); err != nil {
		sugar.Fatalf("work-dir: %v", err)
	}

	rtk := services.NewRTKService(rtklibPath, *workDir, sugar)
	conv := services.NewConverterService(rtklibPath, sugar)

	refCoords := make(map[string][3]float64)
	if *refCSV != "" {
		rc, err := loadRefCoords(*refCSV)
		if err != nil {
			sugar.Fatalf("ref-csv: %v", err)
		}
		refCoords = rc
		sugar.Infof("Эталонных координат: %d пунктов из %s", len(refCoords), *refCSV)
	} else {
		sugar.Infof("--ref-csv не задан: сравнение не выполняется (только PPP)")
	}

	csvFileExists := false
	if info, err := os.Stat(*outCSV); err == nil && info.Size() > 0 {
		csvFileExists = true
	}
	csvFile, err := os.OpenFile(*outCSV, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		sugar.Fatalf("CSV: %v", err)
	}
	defer csvFile.Close()
	csvWriter := csv.NewWriter(csvFile)
	defer csvWriter.Flush()

	if !csvFileExists {
		csvWriter.Write([]string{
			"date", "station", "city", "lat", "lon", "h_m",
			"q", "nsat", "dN_m", "dE_m", "dU_m", "r3d_m", "status", "duration_s",
		})
		csvWriter.Flush()
	}

	done := make(map[string]bool)
	if *resume {
		done = loadDone(*outCSV)
		sugar.Infof("Resume: %d уже обработано", len(done))
	}

	var mu sync.Mutex
	var processed, succeeded int
	var allResults []StationResult
	totalStart := time.Now()

	for _, date := range dates {
		sugar.Infof("День %s: загружаем продукты из %s...", date.Format("2006-01-02"), *workDir)
		prods, err := loadDailyProducts(*workDir, date, sugar)
		if err != nil {
			sugar.Warnf("Пропуск %s: %v", date.Format("2006-01-02"), err)
			continue
		}

		sem := make(chan struct{}, *workers)
		var dayWG sync.WaitGroup

		for i := range catalog {
			st := catalog[i]
			key := date.Format("2006-01-02") + "_" + st.Code
			if done[key] {
				continue
			}
			dayWG.Add(1)
			sem <- struct{}{}
			go func() {
				defer dayWG.Done()
				defer func() { <-sem }()

				res := processStation(st, date, prods, *rgsToken, rtk, conv, refCoords,
					sugar, *workDir, *keepTmp)

				row := []string{
					res.Date.Format("2006-01-02"), res.Code, res.City,
					strconv.FormatFloat(res.Lat, 'f', 8, 64),
					strconv.FormatFloat(res.Lon, 'f', 8, 64),
					strconv.FormatFloat(res.H, 'f', 4, 64),
					strconv.Itoa(res.Q), strconv.Itoa(res.NSat),
					strconv.FormatFloat(res.DN, 'f', 4, 64),
					strconv.FormatFloat(res.DE, 'f', 4, 64),
					strconv.FormatFloat(res.DU, 'f', 4, 64),
					strconv.FormatFloat(res.R3D, 'f', 4, 64),
					res.Status,
					strconv.FormatFloat(res.DurationSec, 'f', 1, 64),
				}

				mu.Lock()
				csvWriter.Write(row)
				allResults = append(allResults, res)
				processed++
				if res.Status == "ok" {
					succeeded++
				}
				if processed%20 == 0 {
					elapsed := time.Since(totalStart).Seconds()
					eta := float64(total-processed) / (float64(processed) / elapsed)
					sugar.Infof("Прогресс: %d/%d (%.1f%%), ok=%d, ETA=%.0fs",
						processed, total, float64(processed)/float64(total)*100, succeeded, eta)
					csvWriter.Flush()
				}
				mu.Unlock()
			}()
		}
		dayWG.Wait()
		csvWriter.Flush()
	}

	sugar.Infof("Генерация отчёта...")
	if err := writeHTMLReport(*outHTML, *outCSV); err != nil {
		sugar.Warnf("HTML: %v", err)
	} else {
		sugar.Infof("Отчёт: %s", *outHTML)
	}

	if *refCSV != "" {
		if err := writeComparisonFile(*outComp, *refCSV, allResults); err != nil {
			sugar.Warnf("Файл сравнения: %v", err)
		} else {
			sugar.Infof("Сравнение: %s", *outComp)
		}
	}

	stats := computeDetailedStats(*outCSV)
	all := stats.Overall
	sep := strings.Repeat("─", 65)
	fmt.Printf("\n%s\n  ФАГС PPP-AR  %s – %s\n%s\n",
		sep, startDate.Format("2006-01-02"), endDate.Format("2006-01-02"), sep)
	fmt.Printf("  Решений: %d  |  Fix: %.1f%%  |  RMS N/E/U/3D: %.2f/%.2f/%.2f/%.2f cm\n",
		all.N, all.FixPct, all.RMS_N*100, all.RMS_E*100, all.RMS_U*100, all.RMS_3D*100)
	fmt.Printf("%s\n  %-12s  %5s  %5s  %5s  %5s  %5s  %5s\n%s\n",
		sep, "Дата", "N", "Fix%", "N,cm", "E,cm", "U,cm", "3D,cm", sep)
	for _, d := range stats.Days {
		fmt.Printf("  %-12s  %5d  %4.1f%%  %5.2f  %5.2f  %5.2f  %5.2f\n",
			d.Label, d.N, d.FixPct, d.RMS_N*100, d.RMS_E*100, d.RMS_U*100, d.RMS_3D*100)
	}
	if len(stats.Months) > 1 {
		fmt.Printf("%s\n  %-12s  %5s  %5s  %5s  %5s  %5s  %5s\n%s\n",
			sep, "Месяц", "N", "Fix%", "N,cm", "E,cm", "U,cm", "3D,cm", sep)
		for _, m := range stats.Months {
			fmt.Printf("  %-12s  %5d  %4.1f%%  %5.2f  %5.2f  %5.2f  %5.2f\n",
				m.Label, m.N, m.FixPct, m.RMS_N*100, m.RMS_E*100, m.RMS_U*100, m.RMS_3D*100)
		}
	}
	fmt.Println(sep)

	// Таблица координат для сравнения
	var okResults []StationResult
	for _, r := range allResults {
		if r.Status == "ok" {
			okResults = append(okResults, r)
		}
	}
	if len(okResults) > 0 {
		sep2 := strings.Repeat("─", 110)
		fmt.Printf("\n%s\n  %-6s  %-4s  %-22s  %-22s  %8s  %7s  %7s\n%s\n",
			sep2, "Пункт", "Q",
			"PPP  B(°)  /  L(°)  /  H(м)",
			"Эталон B(°) / L(°) / H(м)",
			"dN, см", "dE, см", "dU, см", sep2)
		for _, r := range okResults {
			ppp := fmt.Sprintf("%13.8f %13.8f %8.3f", r.Lat, r.Lon, r.H)
			ref := fmt.Sprintf("%13.8f %13.8f %8.3f", r.RefLat, r.RefLon, r.RefH)
			fmt.Printf("  %-6s  %4d  %s  %s  %7.2f  %7.2f  %7.2f\n",
				r.Code, r.Q, ppp, ref,
				r.DN*100, r.DE*100, r.DU*100)
		}
		fmt.Println(sep2)
	}
}
