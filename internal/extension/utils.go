package extension

import (
	"bytes"
	"encoding/json"
	"extension-scaffold/internal/config"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/flare-foundation/go-flare-common/pkg/logger"
	"github.com/flare-foundation/go-flare-common/pkg/tee/instruction"
	teetypes "github.com/flare-foundation/tee-node/pkg/types"
)

// --- In most cases, you will not need to modify this file. ---

func (e *Extension) actionHandler(w http.ResponseWriter, r *http.Request) {
	var action teetypes.Action
	err := json.NewDecoder(r.Body).Decode(&action)
	if err != nil {
		http.Error(w, fmt.Sprintf("decoding action: %v", err), http.StatusBadRequest)
		return
	}

	logger.Infof("received action, ID: %s", action.Data.ID)

	status, body := e.processAction(action)

	logger.Infof("sending action result, ID: %s, status: %d, log: %s", action.Data.ID, status, getLogFromBody(body))

	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func buildResult(a teetypes.Action, df *instruction.DataFixed, data []byte, status uint8, err error) teetypes.ActionResult {
	ar := teetypes.ActionResult{
		ID:            a.Data.ID,
		SubmissionTag: a.Data.SubmissionTag,
		Version:       config.Version,
		OPType:        df.OPType,
		OPCommand:     df.OPCommand,
		Data:          data,
		Status:        status,
	}
	switch status {
	case 0:
		ar.Log = fmt.Sprintf("error: %v", err)
	case 1:
		ar.Log = "ok"
	}
	return ar
}

func getLogFromBody(body []byte) string {
	var ar teetypes.ActionResult
	if err := json.Unmarshal(body, &ar); err != nil {
		return string(body)
	}
	return ar.Log
}

type processorFunc func(teetypes.Action, *instruction.DataFixed) teetypes.ActionResult

func (e *Extension) handleAsync(action teetypes.Action, df *instruction.DataFixed, processor processorFunc) (int, []byte) {
	ar := teetypes.ActionResult{
		ID:            action.Data.ID,
		SubmissionTag: action.Data.SubmissionTag,
		Version:       config.Version,
		OPType:        df.OPType,
		OPCommand:     df.OPCommand,
		Status:        2,
		Log:           "pending",
	}
	pending, _ := json.Marshal(ar)

	go func(a teetypes.Action, d *instruction.DataFixed) {
		result := processor(a, d)
		if err := sendResultWithRetry(e.signPort, result); err != nil {
			logger.Errorf("posting result %s (all retries exhausted): %v", result.ID, err)
		}
	}(action, df)

	return http.StatusOK, pending
}

const (
	resultMaxRetries = 8
	resultBaseDelay  = 1 * time.Second
	resultMaxDelay   = 30 * time.Second
)

func sendResultWithRetry(nodePort int, ar teetypes.ActionResult) error {
	var lastErr error
	delay := resultBaseDelay
	for attempt := 0; attempt <= resultMaxRetries; attempt++ {
		if attempt > 0 {
			logger.Warnf("retrying result delivery %s (attempt %d/%d) in %v", ar.ID, attempt, resultMaxRetries, delay)
			time.Sleep(delay)
			delay *= 2
			if delay > resultMaxDelay {
				delay = resultMaxDelay
			}
		}
		lastErr = sendResult(nodePort, ar)
		if lastErr == nil {
			return nil
		}
		logger.Warnf("result delivery %s attempt %d failed: %v", ar.ID, attempt, lastErr)
	}
	return lastErr
}

func sendResult(nodePort int, ar teetypes.ActionResult) error {
	url := fmt.Sprintf("http://localhost:%d/result", nodePort)
	b, _ := json.Marshal(ar)
	resp, err := http.Post(url, "application/json", bytes.NewReader(b))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("node /result returned %d: %s", resp.StatusCode, string(body))
	}
	return nil
}
