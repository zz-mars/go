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
import "cvmcode"

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

type Resp0 struct {
	code int
	message string
	data string
}

type Resp1 struct {
	code int
	message string
	district string
	data []interface{}
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

// http://gz.cgateway.tencentyun.com/interfaces/interface.php
// http://sh.cgateway.tencentyun.com/interfaces/interface.php
// http://hk.cgateway.tencentyun.com/interfaces/interface.php
// http://ca.cgateway.tencentyun.com/interfaces/interface.php
func getDistrictNameFromInterface(interface_name string) string {
	if len(interface_name) < 9 {
		return "invalid_interface_name"
	}
	return interface_name[7:9]
}

// pack request and send request to cgw
// in any case, there should be a resp written to response channel
// if cgw returns ok, raw data from cgw is stored in resp0.data
func requestInterface(start_idx, end_idx, app_id int, owner_uin, interface_name string, ch chan<- Resp0) {
	resp := Resp0 {
		code: cvmcode.OK,
		message: "ok",
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
	if err != nil {
		resp.code = cvmcode.DO_REQUEST_FAIL
		resp.message = "http client: do request to cgw fail"
		ch <- resp
		return
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		resp.code = cvmcode.CGW_HTTP_RETRUNCODE_NOT_200
		resp.message = "http client: cgw http return code not 200"
		ch <- resp
		return
	}
	body, err := ioutil.ReadAll(response.Body)
	if err != nil {
		resp.code = cvmcode.READ_RESPONSE_FAIL
		resp.message = "http: read response from cgw fail"
		ch <- resp
		return
	}
	resp.data = string(body);
	fmt.Println("response -> ", resp.data)
	// response ok
	// do not parse response here
	// check detailed response info in collecting procedure
	ch <- resp
}

// read from cgw response channel, deal with it
// return = 0 : done
// return > 0 : check if need get remaining cvms
func dealCgwResponse(ch <-chan Resp0, resp1 *Resp1) (int) {
	var resp0 Resp0
	select {
		case resp0 = <-ch:
		case <-time.After(time.Second * 2):
			// timeout : 2 seconds
			// set code & msg
			resp1.code = cvmcode.CGW_TIMEOUT
			resp1.message = "cgw timeout"
			return 0
	}
	resp1.code = resp0.code
	resp1.message = resp0.message
	// directly return if any error occurs in http request
	if resp1.code != cvmcode.OK {
		return 0
	}
	// parse cgw response
	cgw_jdata, err := sjson.NewJson([]byte(resp0.data))
	if err != nil {
		resp1.code = cvmcode.PARSE_CGW_RESPONSE__FAIL
		resp1.message = "parse cgw response fail"
		return 0
	}
	resp1.code = cgw_jdata.Get("returnCode").MustInt(cvmcode.CGW_NO_RETURNCODE)
	// directly return if cgw returns error
	if resp1.code != cvmcode.OK {
		resp1.message = cgw_jdata.Get("returnMessage").MustString("on return msg")
		return 0
	}
	// get cvm data
	cvm_lists := cgw_jdata.GetPath("data", "deviceList").MustArray(make([]interface{},0));
	// append to cvm list
	for _, cvm := range cvm_lists {
		resp1.data = append(resp1.data, cvm)
	}
	// return total number
	return cgw_jdata.GetPath("data", "totalNum").MustInt(0)
}

// return 4 slices
// code message district devicelist
func processGetCvmList(app_id int, owner_uin, district string) ([]int, []string, []string, []interface{}) {
	code_list := make([]int, 0)
	message_list := make([]string, 0)
	district_list := make([]string, 0)
    device_list := make([]interface{}, 0)
	// calling all the interfaces if district == "all"
	var interface_list []string
	switch district {
		case "gz":
			interface_list = []string{cgw_conf.Gz}
		case "sh":
			interface_list = []string{cgw_conf.Sh}
		case "hk":
			interface_list = []string{cgw_conf.Hk}
		case "ca":
			interface_list = []string{cgw_conf.Ca}
		case "all":
			interface_list = []string{cgw_conf.Gz, cgw_conf.Sh, cgw_conf.Hk, cgw_conf.Ca}
		default:
			// return empty list
			return code_list, message_list, district_list, device_list
	}
	interface_num := len(interface_list)
	// wait for all the interface to return
	collect_ch := make(chan Resp1, interface_num)
	var wait_group sync.WaitGroup
	wait_group.Add(interface_num)
	// calling multiple interfaces concurrently
	for _, interface_name := range interface_list {
		// each with a single goroutine
		go func(interface_name string) {
			defer wait_group.Done()
			// get the first 1024 cvms
			start_idx := 0
			end_idx := 1023
			// for timeout control and result retrive
			ch := make(chan Resp0, 1)
			// POST request in goroutine
			// response should be written to ch
			go requestInterface(start_idx, end_idx, app_id, owner_uin, interface_name, ch)
			// no data
			resp1 := Resp1 {
				district: getDistrictNameFromInterface(interface_name),
				data: make([]interface{}, 0),
			}
			// expect response from ch
			total_cvm_num := dealCgwResponse(ch, &resp1)
			start_idx = end_idx + 1
			end_idx = total_cvm_num - 1
			// no more cvms
			if end_idx < start_idx {
				collect_ch <- resp1
				return
			}
			// get the ramaining cvms in another goroutine
			go requestInterface(start_idx, end_idx, app_id, owner_uin, interface_name, ch)
			dealCgwResponse(ch, &resp1)
			// response anyway
			collect_ch <- resp1
		} (interface_name)
	}
	// wait for all the interface to return
	wait_group.Wait()
	collect_result_done := false
	// iterate until all data are collected
	for collect_result_done==false {
		select {
			case final_result := <-collect_ch:
				fmt.Println("collect -> ", final_result)
				// collect cvms in data
				for _, cvm := range final_result.data {
					device_list = append(device_list, cvm)
				}
				// collect code & message & district
				code_list = append(code_list, final_result.code)
				message_list = append(message_list, final_result.message)
				district_list = append(district_list, final_result.district)
			default:
				// done
				collect_result_done = true
		}
	}
    return code_list, message_list, district_list, device_list
}

func packResponse(returnCode int, returnMessage string, code_list []int, message_list, district_list []string, data interface{}) []byte{
    resp := sjson.New()
	resp.Set("returnCode", returnCode)
	resp.Set("returnMessage", returnMessage)
	resp.Set("data", data)
	resp.Set("codes", code_list)
	resp.Set("msgs", message_list)
	resp.Set("districts", district_list)
	resp_str, err := resp.EncodePretty()
	if err != nil {
		fmt.Println("EncodePretty error")
		resp_str = []byte("EncodePretty error")
	}
	return resp_str
}

func getCvmList(w http.ResponseWriter, req *http.Request) {
	// parse parameters
    app_id, owner_uin, district := parseParams(req)
	var resp_str []byte
	if app_id < 0 {
		// fail
		resp_str = packResponse(cvmcode.PARAM_ERR, "param error", nil, nil, nil, nil);
		w.Write(resp_str)
		return
	}
    //s := fmt.Sprintf("%d : %d : %s\n", app_id, owner_uin, district)
	// expect cvm list
    code_list, message_list, district_list, device_list := processGetCvmList(app_id, owner_uin, district)
	fmt.Println("final code_list => ", code_list)
	fmt.Println("final message_list => ", message_list)
	fmt.Println("final district_list => ", district_list)
	fmt.Println("final device_list => ", device_list)
	final_return_code := cvmcode.CGW_INTERFACE_ALL_FAIL
	final_return_msg := "all cgw interface failed"
	// return ok if there are any interface returns ok
	for _, rt_code := range code_list {
		if rt_code == cvmcode.OK {
			final_return_code = cvmcode.OK
			final_return_msg = "ok"
			break
		}
	}
	device_num := len(device_list)
	cvms := sjson.New()
	cvms.Set("totalNum", device_num)
	cvms.Set("deviceList", device_list)
	// response
	resp_str = packResponse(final_return_code, final_return_msg, code_list, message_list, district_list, cvms)
    w.Write(resp_str)
}

func main() {
    http.HandleFunc("/getcvmlist", getCvmList)
    http.ListenAndServe(":10241", nil)
}
