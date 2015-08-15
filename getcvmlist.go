package main

import "fmt"
import "strconv"
import "net/http"
import "encoding/json"
import sjson "go-simplejson"
import "io/ioutil"
import "os"
import "runtime"
import "sync"
import "time"
import "math/rand"
import "bytes"

type CgwConfig struct {
	Gz string	// guangzhou
	Sh string	// shanghai
	Hk string	// hongkong
	Ca string	// north america
	CgwTimeout int	// timeout for cgw request
}

// cgw config
var cgw_conf CgwConfig
var cgw_timeout int

//var cgw_conf map[string] string

func init() {
    default_config_file := "cgw.conf"
	conf_data, err := ioutil.ReadFile(default_config_file)
	if err != nil {
		fmt.Printf("read config file fail : %s\n", err)
		os.Exit(1)
	}
	if err := json.Unmarshal(conf_data, &cgw_conf); err != nil {
		fmt.Printf("decode config file fail : %s\n", err)
		os.Exit(1)
	}
	cgw_timeout = cgw_conf.CgwTimeout
	fmt.Println(cgw_conf.Gz)
	fmt.Println(cgw_conf.Sh)
	fmt.Println(cgw_conf.Hk)
	fmt.Println(cgw_conf.Ca)
	fmt.Println(cgw_conf.CgwTimeout)
//	conf_json, err := sjson.NewJson(conf_data)
//	if err != nil {
//		fmt.Printf("new json fail : %s\n", err)
//		os.Exit(1)
//	}
//	map_conf, err := conf_json.Map()
//	if err != nil {
//		fmt.Printf("json Map fail : %s\n", err)
//		os.Exit(1)
//	}
//	cgw_conf = map_conf
//	for dist, cgw_interface := range cgw_conf {
//		fmt.Println(dist, " => ", cgw_interface)
//	}
	runtime.GOMAXPROCS(runtime.NumCPU())
}


func parseParams(r *http.Request) (int, string, string) {
    r.ParseForm()
    app_id, err := strconv.Atoi(r.FormValue("app_id"))
    if err != nil {
        return -1, "", ""
    }
    owner_uin := r.FormValue("owner_uin")
    district := r.FormValue("district")
    return app_id, owner_uin, district
}

type CgwResp struct {
	code int
	message string
	data interface{}
}

func preparePostData(start_idx, end_idx, app_id int, owner_uin string) (string, int) {
	// params
	params := sjson.New()
    params.Set("appId", app_id)
    params.Set("uin", owner_uin)
    params.Set("startNum", start_idx)
    params.Set("endNum", end_idx)
    params.Set("simplify", 1)
    params.Set("allType", 1)
	// interface data
	interface_data := sjson.New()
	interface_data.Set("interfaceName", "qcloud.Qcvm.getCvmList")
	interface_data.Set("para", params)
	// post data in json format
	post_data := sjson.New()
	// unix timestamp
	cur_time := time.Now().Unix()
	// random number as event id
	eventId := rand.New(rand.NewSource(cur_time)).Intn(6553500)
	post_data.Set("version", "1.0")
	post_data.Set("caller", "CGW")
	post_data.Set("callee", "CGW")
	post_data.Set("eventId", eventId)
	post_data.Set("timestamp", cur_time)
	post_data.Set("interface", interface_data)
	post_data.Set("postOperation", make([]string, 0))
	bytes, err := post_data.Encode()
	if err != nil {
		return "", 1
	}
	return string(bytes), 0
}

// pack request and send request to cgw
// in any case, there should be a resp written to response channel
func requestInterface(start_idx, end_idx, app_id int, owner_uin, interface_name string, ch chan<- CgwResp) {
	resp := CgwResp {
		data: "",
	}
	// prepare request data
	post_data, errcode := preparePostData(start_idx, end_idx, app_id, owner_uin)
	if errcode != 0 {
		resp.code = cvmcode.PREPARE_POST_DATA_FAIL
		resp.message = "prepare post data fail"
		ch <- resp
		return
	}
	fmt.Println("request -> ", post_data)
	postBytesReader := bytes.NewReader([]byte(post_data))
	// http client without timeout
    client := &http.Client{}
//    client := &http.Client{
//		Timeout: cgw_conf.CgwTimeout}
	request, err := http.NewRequest("POST", interface_name, postBytesReader)
	if err != nil {
		resp.code = cvmcode.NEWREQUEST_FAIL
		resp.message = "http.NewRequest fail"
		ch <- resp
		return
	}
	response, err := client.Do(request)
	if err != nil || response.StatusCode != 200 {
		resp.code = cvmcode.DO_REQUEST_FAIL
		resp.message = "http client: do request to cgw fail"
		ch <- resp
		return
	}
	defer response.Body.Close()
	body, err := ioutil.ReadAll(response.Body)
	if err != nil {
		resp.code = cvmcode.READ_RESPONSE_FAIL
		resp.message = "http: read response from cgw fail"
		ch <- resp
		return
	}
	bodystr := string(body);
	fmt.Println("response -> ", bodystr)
	// parse response and make resp
	resp_jdata, err := sjson.NewJson([]byte(bodystr))
	if err != nil {
		resp.code = cvmcode.PARSE_CGW_RESPONSE__FAIL
		resp.message = "cgw response is not json"
		ch <- resp
		return
	}
	if 
	ch <- resp
}

func processGetCvmList(app_id int, owner_uin, district string) []interface{} {
    device_list := make([]interface{}, 0)
	// calling all the interfaces if district == "all"
	interface_list := make([]string, 0)
	switch district {
		case "gz":
			interface_list = append(interface_list, cgw_conf.Gz)
		case "sh":
			interface_list = append(interface_list, cgw_conf.Sh)
		case "hk":
			interface_list = append(interface_list, cgw_conf.Hk)
		case "ca":
			interface_list = append(interface_list, cgw_conf.Ca)
		case "all":
			interface_list = append(interface_list, cgw_conf.Gz, cgw_conf.Sh, cgw_conf.Hk, cgw_conf.Ca)
		default:
			// return empty device list
			return device_list
	}
	interface_num := len(interface_list)
	// wait for all the interface to return
	collect_ch := make(chan Resp, interface_num)
	var wait_group sync.WaitGroup
	wait_group.Add(interface_num)
	// calling multiple interfaces concurrently
	for _, interface_name := range interface_list {
		// each with a single goroutine
		go func(interface_name string) {
			defer wait_group.Done()
			start_idx := 0
			end_idx := 1024
			// for timeout control and result retrive
			ch := make(chan Resp, 1)
			// POST request in goroutine
			go requestInterface(start_idx, end_idx, app_id, owner_uin, interface_name, ch)
			select {
				case resp := <-ch:
					// collect response
					collect_ch <- resp
				case <-time.After(time.Second * 2):
					// timeout : 2 seconds
					return
			}
			device_list = append(device_list, interface_name)
		} (interface_name)
	}
	// wait for all the interface to return
	wait_group.Wait()
	collect_result_done := false
	// iterate until all data are collected
	for collect_result_done==false {
		select {
			case final_result := <-collect_ch:
				fmt.Println(final_result)
			default:
				// done
				collect_result_done = true
		}
	}
	fmt.Println("done")
    return device_list
}

func packResponse(returnCode int, returnMessage string, data interface{}) []byte{
    resp := sjson.New()
	resp.Set("returnCode", returnCode)
	resp.Set("returnMessage", returnMessage)
	resp.Set("data", data)
	resp_str, err := resp.EncodePretty()
	if err != nil {
		fmt.Println("EncodePretty error")
		resp_str = []byte("EncodePretty error")
	}
	return resp_str
}

func getCvmList(w http.ResponseWriter, req *http.Request) {
    app_id, owner_uin, district := parseParams(req)
	var resp_str []byte
	if app_id < 0 {
		resp_str = packResponse(errcode.PARAM_ERR, "param error", nil);
		w.Write(resp_str)
		return
	}
    //s := fmt.Sprintf("%d : %d : %s\n", app_id, owner_uin, district)
    device_list := processGetCvmList(app_id, owner_uin, district)
	device_num := len(device_list)
	cvms := sjson.New()
	cvms.Set("totalNum", device_num)
	cvms.Set("deviceList", device_list)
	resp_str = packResponse(cvmcode.OK, "ok", cvms)
    w.Write(resp_str)
}

func main() {
    http.HandleFunc("/getcvmlist", getCvmList)
    http.ListenAndServe(":10241", nil)
}
