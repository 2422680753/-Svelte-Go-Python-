import cv2
import numpy as np
from typing import List, Dict, Any, Tuple
from dataclasses import dataclass
import logging

logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)


@dataclass
class ImageAnalysisResult:
    is_flagged: bool
    score: float
    violations: List[str]
    details: Dict[str, Any]


class ImageAnalyzer:
    def __init__(self):
        self.skin_ranges = [
            {"lower": np.array([0, 20, 70], dtype=np.uint8), "upper": np.array([20, 255, 255], dtype=np.uint8)},
            {"lower": np.array([160, 20, 70], dtype=np.uint8), "upper": np.array([180, 255, 255], dtype=np.uint8)},
        ]
        
        self.red_ranges = [
            {"lower": np.array([0, 50, 50], dtype=np.uint8), "upper": np.array([10, 255, 255], dtype=np.uint8)},
            {"lower": np.array([170, 50, 50], dtype=np.uint8), "upper": np.array([180, 255, 255], dtype=np.uint8)},
        ]
        
    def analyze(self, image: np.ndarray) -> ImageAnalysisResult:
        """分析单张图像"""
        h, w = image.shape[:2]
        
        hsv = cv2.cvtColor(image, cv2.COLOR_BGR2HSV)
        gray = cv2.cvtColor(image, cv2.COLOR_BGR2GRAY)
        
        skin_ratio = self._detect_skin(hsv, h, w)
        red_ratio = self._detect_red(hsv, h, w)
        brightness = np.mean(gray) / 255.0
        contrast = np.std(gray) / 255.0
        edge_density = self._detect_edges(gray)
        face_count = self._detect_faces(image)
        
        scores = {
            "skin": 0.0,
            "blood": 0.0,
            "darkness": 0.0,
            "blurry": 0.0
        }
        
        violations = []
        total_score = 0.1
        is_flagged = False
        
        if skin_ratio > 0.6:
            scores["skin"] = 0.85
            violations.append("nudity")
            is_flagged = True
        elif skin_ratio > 0.45:
            scores["skin"] = 0.6
        elif skin_ratio > 0.3:
            scores["skin"] = 0.3
            
        if red_ratio > 0.4:
            scores["blood"] = 0.75
            violations.append("violence")
            is_flagged = True
        elif red_ratio > 0.25:
            scores["blood"] = 0.4
        elif red_ratio > 0.15:
            scores["blood"] = 0.2
            
        if brightness < 0.15 or brightness > 0.95:
            scores["darkness"] = 0.3
            
        if contrast < 0.15:
            scores["blurry"] = 0.2
            
        if face_count > 5:
            scores["crowd"] = 0.2
            
        total_score = max(scores.values())
        
        details = {
            "width": w,
            "height": h,
            "aspect_ratio": w / h if h > 0 else 0,
            "skin_ratio": float(skin_ratio),
            "red_ratio": float(red_ratio),
            "brightness": float(brightness),
            "contrast": float(contrast),
            "edge_density": float(edge_density),
            "face_count": face_count,
            "category_scores": scores
        }
        
        return ImageAnalysisResult(
            is_flagged=is_flagged,
            score=total_score,
            violations=violations,
            details=details
        )
        
    def _detect_skin(self, hsv: np.ndarray, h: int, w: int) -> float:
        """检测皮肤区域"""
        total_mask = np.zeros((h, w), dtype=np.uint8)
        
        for skin_range in self.skin_ranges:
            mask = cv2.inRange(hsv, skin_range["lower"], skin_range["upper"])
            total_mask = cv2.bitwise_or(total_mask, mask)
            
        kernel = cv2.getStructuringElement(cv2.MORPH_ELLIPSE, (5, 5))
        total_mask = cv2.morphologyEx(total_mask, cv2.MORPH_OPEN, kernel)
        total_mask = cv2.morphologyEx(total_mask, cv2.MORPH_CLOSE, kernel)
        
        skin_pixels = np.sum(total_mask > 0)
        return skin_pixels / (h * w)
        
    def _detect_red(self, hsv: np.ndarray, h: int, w: int) -> float:
        """检测红色区域（可能是血液）"""
        total_mask = np.zeros((h, w), dtype=np.uint8)
        
        for red_range in self.red_ranges:
            mask = cv2.inRange(hsv, red_range["lower"], red_range["upper"])
            total_mask = cv2.bitwise_or(total_mask, mask)
            
        red_pixels = np.sum(total_mask > 0)
        return red_pixels / (h * w)
        
    def _detect_edges(self, gray: np.ndarray) -> float:
        """检测边缘密度"""
        edges = cv2.Canny(gray, 50, 150)
        edge_pixels = np.sum(edges > 0)
        h, w = gray.shape[:2]
        return edge_pixels / (h * w)
        
    def _detect_faces(self, image: np.ndarray) -> int:
        """检测人脸数量"""
        h, w = image.shape[:2]
        
        try:
            face_cascade = cv2.CascadeClassifier(
                cv2.data.haarcascades + 'haarcascade_frontalface_default.xml'
            )
            
            if face_cascade.empty():
                return 0
                
            gray = cv2.cvtColor(image, cv2.COLOR_BGR2GRAY)
            faces = face_cascade.detectMultiScale(
                gray,
                scaleFactor=1.1,
                minNeighbors=5,
                minSize=(30, 30)
            )
            
            return len(faces)
        except Exception as e:
            logger.warning(f"Face detection failed: {e}")
            return 0
            
    def analyze_frames(self, frames: List[Dict[str, Any]]) -> Dict[str, Any]:
        """分析多个视频帧"""
        results = []
        total_score = 0.0
        all_violations = set()
        flagged_frames = []
        
        for frame_data in frames:
            frame_idx = frame_data.get("frame_index", 0)
            timestamp = frame_data.get("timestamp", 0.0)
            is_flagged = frame_data.get("is_flagged", False)
            score = frame_data.get("score", 0.0)
            violations = frame_data.get("violations", [])
            details = frame_data.get("details", {})
            
            result = {
                "frame_index": frame_idx,
                "timestamp": timestamp,
                "is_flagged": is_flagged,
                "score": score,
                "violations": violations,
                "details": details
            }
            
            results.append(result)
            total_score = max(total_score, score)
            
            if violations:
                all_violations.update(violations)
                
            if is_flagged:
                flagged_frames.append({
                    "frame_index": frame_idx,
                    "timestamp": timestamp,
                    "score": score,
                    "violations": violations
                })
                
        return {
            "frames": results,
            "flagged_frames": flagged_frames,
            "max_score": total_score,
            "violations": list(all_violations),
            "frame_count": len(results),
            "flagged_count": len(flagged_frames)
        }
