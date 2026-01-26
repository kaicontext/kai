package metrics

import "expvar"

var (
	HTTPRequests     = expvar.NewMap("kailab_http_requests_total")
	HTTPErrors       = expvar.NewMap("kailab_http_errors_total")
	HTTPStatusCounts = expvar.NewMap("kailab_http_status_total")
	SSHUploadPack    = expvar.NewInt("kailab_ssh_git_upload_pack_total")
	SSHReceivePack   = expvar.NewInt("kailab_ssh_git_receive_pack_total")
	SSHErrors        = expvar.NewInt("kailab_ssh_git_errors_total")
)

func IncHTTP(method, route string, status int) {
	key := method + " " + route
	HTTPRequests.Add(key, 1)
	HTTPStatusCounts.Add(key+" "+statusLabel(status), 1)
	if status >= 400 {
		HTTPErrors.Add(key, 1)
	}
}

func IncSSHUploadPack() {
	SSHUploadPack.Add(1)
}

func IncSSHReceivePack() {
	SSHReceivePack.Add(1)
}

func IncSSHErrors() {
	SSHErrors.Add(1)
}

func statusLabel(code int) string {
	if code < 0 {
		return "unknown"
	}
	return "status_" + itoa(code)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [12]byte
	i := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
