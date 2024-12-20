package processor

import (
	"sync/atomic"
	"time"
)

type ProcessorMetrics struct {
	TotalFramesProcessed     uint64
	TotalVideosGenerated     uint64
	TotalProcessingErrors    uint64
	LastFrameProcessedTimeNs int64
	LastVideoGeneratedTimeNs int64
	AverageProcessingTime    time.Duration
	ProcessingTimeSum        int64
	ProcessingTimeCount      uint64
}

func (pm *ProcessorMetrics) RecordFrameProcessed(processingTime time.Duration) {
	atomic.AddUint64(&pm.TotalFramesProcessed, 1)
	atomic.StoreInt64(&pm.LastFrameProcessedTimeNs, time.Now().UnixNano())

	// Update average processing time
	atomic.AddInt64((*int64)(&pm.ProcessingTimeSum), int64(processingTime))
	count := atomic.AddUint64(&pm.ProcessingTimeCount, 1)
	if count > 0 {
		avgTime := time.Duration(atomic.LoadInt64((*int64)(&pm.ProcessingTimeSum))) / time.Duration(count)
		atomic.StoreInt64((*int64)(&pm.AverageProcessingTime), int64(avgTime))
	}
}

func (pm *ProcessorMetrics) RecordVideoGenerated() {
	atomic.AddUint64(&pm.TotalVideosGenerated, 1)
	atomic.StoreInt64(&pm.LastVideoGeneratedTimeNs, time.Now().UnixNano())
}

func (pm *ProcessorMetrics) RecordError() {
	atomic.AddUint64(&pm.TotalProcessingErrors, 1)
}

func (pm *ProcessorMetrics) GetMetrics() map[string]interface{} {
	return map[string]interface{}{
		"total_frames_processed":     atomic.LoadUint64(&pm.TotalFramesProcessed),
		"total_videos_generated":     atomic.LoadUint64(&pm.TotalVideosGenerated),
		"total_processing_errors":    atomic.LoadUint64(&pm.TotalProcessingErrors),
		"last_frame_processed_time":  time.Unix(0, atomic.LoadInt64(&pm.LastFrameProcessedTimeNs)),
		"last_video_generated_time":  time.Unix(0, atomic.LoadInt64(&pm.LastVideoGeneratedTimeNs)),
		"average_processing_time_ms": time.Duration(atomic.LoadInt64((*int64)(&pm.AverageProcessingTime))).Milliseconds(),
	}
}
