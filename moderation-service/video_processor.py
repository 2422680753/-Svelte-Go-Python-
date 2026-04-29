import cv2
import numpy as np
import os
import tempfile
import requests
from typing import List, Dict, Any, Optional
from dataclasses import dataclass
import logging

logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)


@dataclass
class FrameResult:
    frame_index: int
    timestamp: float
    is_flagged: bool
    score: float
    violations: List[str]
    details: Dict[str, Any]


class VideoProcessor:
    def __init__(self, frame_interval: int = 5, temp_dir: Optional[str] = None):
        self.frame_interval = frame_interval
        self.temp_dir = temp_dir or tempfile.gettempdir()
        self.sensitive_keywords = [
            "暴力", "血腥", "色情", "裸", "性", "恐怖", "惊悚",
            "赌博", "毒品", "枪支", "刀", "杀人", "打", "强奸",
            "violence", "blood", "porn", "nude", "sex", "terror",
            "gamble", "drug", "gun", "kill", "rape"
        ]
        
    def download_video(self, video_url: str, video_id: str) -> str:
        """下载视频到本地临时目录"""
        local_path = os.path.join(self.temp_dir, f"{video_id}.mp4")
        
        if os.path.exists(local_path):
            return local_path
            
        try:
            response = requests.get(video_url, stream=True, timeout=60)
            response.raise_for_status()
            
            with open(local_path, 'wb') as f:
                for chunk in response.iter_content(chunk_size=8192):
                    if chunk:
                        f.write(chunk)
                        
            logger.info(f"Video downloaded to {local_path}")
            return local_path
            
        except Exception as e:
            logger.error(f"Failed to download video: {e}")
            raise
            
    def extract_frames(self, video_path: str, video_id: str) -> List[FrameResult]:
        """从视频中提取关键帧"""
        if not os.path.exists(video_path):
            raise FileNotFoundError(f"Video file not found: {video_path}")
            
        cap = cv2.VideoCapture(video_path)
        if not cap.isOpened():
            raise ValueError(f"Failed to open video: {video_path}")
            
        fps = cap.get(cv2.CAP_PROP_FPS)
        total_frames = int(cap.get(cv2.CAP_PROP_FRAME_COUNT))
        duration = total_frames / fps if fps > 0 else 0
        
        frame_interval_frames = int(fps * self.frame_interval) if fps > 0 else 30
        
        frame_results = []
        frame_index = 0
        
        while True:
            ret, frame = cap.read()
            if not ret:
                break
                
            if frame_index % frame_interval_frames == 0:
                timestamp = frame_index / fps if fps > 0 else frame_index * 0.033
                
                analysis_result = self.analyze_frame(frame, frame_index)
                frame_results.append(analysis_result)
                
                frame_dir = os.path.join(self.temp_dir, "frames", video_id)
                os.makedirs(frame_dir, exist_ok=True)
                frame_path = os.path.join(frame_dir, f"frame_{frame_index}.jpg")
                cv2.imwrite(frame_path, frame)
                
            frame_index += 1
            
        cap.release()
        logger.info(f"Extracted {len(frame_results)} frames from video")
        return frame_results
        
    def analyze_frame(self, frame: np.ndarray, frame_index: int) -> FrameResult:
        """分析单帧图像"""
        h, w = frame.shape[:2]
        
        gray = cv2.cvtColor(frame, cv2.COLOR_BGR2GRAY)
        avg_brightness = np.mean(gray) / 255.0
        
        hsv = cv2.cvtColor(frame, cv2.COLOR_BGR2HSV)
        avg_hue = np.mean(hsv[:, :, 0])
        avg_saturation = np.mean(hsv[:, :, 1]) / 255.0
        
        skin_lower = np.array([0, 20, 70], dtype=np.uint8)
        skin_upper = np.array([20, 255, 255], dtype=np.uint8)
        skin_mask = cv2.inRange(hsv, skin_lower, skin_upper)
        skin_ratio = np.sum(skin_mask > 0) / (h * w)
        
        red_lower1 = np.array([0, 50, 50], dtype=np.uint8)
        red_upper1 = np.array([10, 255, 255], dtype=np.uint8)
        red_lower2 = np.array([170, 50, 50], dtype=np.uint8)
        red_upper2 = np.array([180, 255, 255], dtype=np.uint8)
        red_mask1 = cv2.inRange(hsv, red_lower1, red_upper1)
        red_mask2 = cv2.inRange(hsv, red_lower2, red_upper2)
        red_mask = cv2.bitwise_or(red_mask1, red_mask2)
        red_ratio = np.sum(red_mask > 0) / (h * w)
        
        scores = {
            "brightness": 0.0,
            "skin_ratio": 0.0,
            "red_ratio": 0.0,
            "contrast": 0.0
        }
        
        violations = []
        is_flagged = False
        total_score = 0.1
        
        if avg_brightness < 0.1 or avg_brightness > 0.95:
            scores["brightness"] = 0.5
            total_score = max(total_score, 0.5)
            
        if skin_ratio > 0.6:
            scores["skin_ratio"] = 0.8
            violations.append("nudity")
            is_flagged = True
            total_score = max(total_score, 0.8)
        elif skin_ratio > 0.4:
            scores["skin_ratio"] = 0.5
            total_score = max(total_score, 0.5)
            
        if red_ratio > 0.5:
            scores["red_ratio"] = 0.7
            violations.append("violence")
            is_flagged = True
            total_score = max(total_score, 0.7)
        elif red_ratio > 0.3:
            scores["red_ratio"] = 0.4
            total_score = max(total_score, 0.4)
            
        contrast = np.std(gray) / 255.0
        if contrast > 0.8:
            scores["contrast"] = 0.3
            total_score = max(total_score, 0.3)
            
        details = {
            "width": w,
            "height": h,
            "avg_brightness": float(avg_brightness),
            "avg_hue": float(avg_hue),
            "avg_saturation": float(avg_saturation),
            "skin_ratio": float(skin_ratio),
            "red_ratio": float(red_ratio),
            "contrast": float(contrast),
            "scores": scores
        }
        
        return FrameResult(
            frame_index=frame_index,
            timestamp=frame_index * self.frame_interval,
            is_flagged=is_flagged,
            score=total_score,
            violations=violations,
            details=details
        )
        
    def process_video(self, video_url: str, video_id: str) -> Dict[str, Any]:
        """处理整个视频，返回分析结果"""
        try:
            local_path = self.download_video(video_url, video_id)
            frames = self.extract_frames(local_path, video_id)
            
            flagged_frames = [f for f in frames if f.is_flagged]
            max_score = max([f.score for f in frames]) if frames else 0.0
            
            violations = set()
            for f in flagged_frames:
                violations.update(f.violations)
                
            return {
                "frames": [
                    {
                        "frame_index": f.frame_index,
                        "timestamp": f.timestamp,
                        "is_flagged": f.is_flagged,
                        "score": f.score,
                        "violations": f.violations,
                        "details": f.details
                    }
                    for f in frames
                ],
                "flagged_frames": [
                    {
                        "frame_index": f.frame_index,
                        "timestamp": f.timestamp,
                        "score": f.score,
                        "violations": f.violations
                    }
                    for f in flagged_frames
                ],
                "max_score": max_score,
                "violations": list(violations),
                "frame_count": len(frames),
                "flagged_count": len(flagged_frames)
            }
            
        except Exception as e:
            logger.error(f"Error processing video {video_id}: {e}")
            return {
                "frames": [],
                "flagged_frames": [],
                "max_score": 0.0,
                "violations": [],
                "frame_count": 0,
                "flagged_count": 0,
                "error": str(e)
            }
