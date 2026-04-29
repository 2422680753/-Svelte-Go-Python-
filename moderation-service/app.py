from flask import Flask, request, jsonify
from flask_cors import CORS
import os
import time
import logging
from dotenv import load_dotenv

from video_processor import VideoProcessor
from text_analyzer import TextAnalyzer
from image_analyzer import ImageAnalyzer

load_dotenv()

logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)

app = Flask(__name__)
CORS(app)

frame_interval = int(os.getenv("FRAME_INTERVAL", "5"))
auto_reject_threshold = float(os.getenv("AUTO_REJECT_THRESHOLD", "0.9"))
auto_approve_threshold = float(os.getenv("AUTO_APPROVE_THRESHOLD", "0.1"))
need_review_threshold = float(os.getenv("NEED_REVIEW_THRESHOLD", "0.5"))

video_processor = VideoProcessor(frame_interval=frame_interval)
text_analyzer = TextAnalyzer()
image_analyzer = ImageAnalyzer()


@app.route("/health", methods=["GET"])
def health_check():
    return jsonify({
        "status": "healthy",
        "service": "content-moderation-service",
        "timestamp": time.time()
    })


@app.route("/api/v1/moderate", methods=["POST"])
def moderate_content():
    """
    综合内容审核接口
    接收视频信息和已提取的帧，进行综合分析
    """
    start_time = time.time()
    
    try:
        data = request.get_json()
        if not data:
            return jsonify({"error": "No JSON data provided"}), 400
            
        video_id = data.get("video_id")
        video_url = data.get("video_url")
        title = data.get("title", "")
        description = data.get("description", "")
        frames = data.get("frames", [])
        
        logger.info(f"Processing moderation for video: {video_id}")
        
        text_scores = {}
        text_violations = []
        text_keywords = []
        
        if title:
            title_result = text_analyzer.analyze(title)
            text_scores["title"] = title_result.score
            text_violations.extend(title_result.violations)
            text_keywords.extend(title_result.keywords)
            
        if description:
            desc_result = text_analyzer.analyze(description)
            text_scores["description"] = desc_result.score
            text_violations.extend(desc_result.violations)
            text_keywords.extend(desc_result.keywords)
            
        text_analysis = {
            "text": f"{title} {description}".strip(),
            "score": max(text_scores.values()) if text_scores else 0.0,
            "violations": list(set(text_violations)),
            "keywords": list(set(text_keywords)),
            "details": {
                "title_score": text_scores.get("title", 0.0),
                "description_score": text_scores.get("description", 0.0)
            }
        }
        
        frame_analysis = image_analyzer.analyze_frames(frames)
        
        violation_scores = {
            "violence": 0.0,
            "nudity": 0.0,
            "hate_speech": 0.0,
            "misinformation": 0.0,
            "sensitive": 0.0,
            "gambling": 0.0,
            "drugs": 0.0,
            "politics": 0.0,
            "fraud": 0.0
        }
        
        for violation in frame_analysis.get("violations", []):
            if violation == "violence":
                violation_scores["violence"] = frame_analysis.get("max_score", 0.0)
            elif violation == "nudity":
                violation_scores["nudity"] = frame_analysis.get("max_score", 0.0)
                
        for violation in text_analysis.get("violations", []):
            if violation in violation_scores:
                violation_scores[violation] = max(
                    violation_scores[violation],
                    text_analysis.get("score", 0.0)
                )
                
        overall_score = max(
            frame_analysis.get("max_score", 0.0),
            text_analysis.get("score", 0.0)
        )
        
        recommendation = get_recommendation(overall_score)
        
        flagged_frames = frame_analysis.get("flagged_frames", [])
        
        processing_time = time.time() - start_time
        
        response = {
            "video_id": video_id,
            "overall_score": overall_score,
            "violation_scores": violation_scores,
            "flagged_frames": flagged_frames,
            "text_analysis": text_analysis,
            "audio_analysis": {
                "score": 0.1,
                "violations": [],
                "transcript": "",
                "details": {}
            },
            "recommendation": recommendation,
            "processing_time": processing_time
        }
        
        logger.info(f"Moderation completed for video {video_id}: score={overall_score}, recommendation={recommendation}")
        
        return jsonify(response)
        
    except Exception as e:
        logger.error(f"Error during moderation: {e}", exc_info=True)
        return jsonify({"error": str(e)}), 500


@app.route("/api/v1/analyze-text", methods=["POST"])
def analyze_text():
    """仅分析文本内容"""
    try:
        data = request.get_json()
        if not data or "text" not in data:
            return jsonify({"error": "Text is required"}), 400
            
        text = data["text"]
        result = text_analyzer.analyze(text)
        
        return jsonify({
            "text": result.text,
            "score": result.score,
            "violations": result.violations,
            "keywords": result.keywords,
            "details": result.details
        })
        
    except Exception as e:
        logger.error(f"Error analyzing text: {e}")
        return jsonify({"error": str(e)}), 500


@app.route("/api/v1/analyze-frames", methods=["POST"])
def analyze_frames():
    """分析视频帧"""
    try:
        data = request.get_json()
        if not data or "frames" not in data:
            return jsonify({"error": "Frames data is required"}), 400
            
        frames = data["frames"]
        result = image_analyzer.analyze_frames(frames)
        
        return jsonify(result)
        
    except Exception as e:
        logger.error(f"Error analyzing frames: {e}")
        return jsonify({"error": str(e)}), 500


@app.route("/api/v1/extract-frames", methods=["POST"])
def extract_frames():
    """从视频URL提取帧"""
    try:
        data = request.get_json()
        if not data or "video_url" not in data:
            return jsonify({"error": "Video URL is required"}), 400
            
        video_url = data["video_url"]
        video_id = data.get("video_id", str(time.time()))
        
        result = video_processor.process_video(video_url, video_id)
        
        return jsonify(result)
        
    except Exception as e:
        logger.error(f"Error extracting frames: {e}")
        return jsonify({"error": str(e)}), 500


def get_recommendation(score: float) -> str:
    """根据得分计算推荐结果"""
    if score >= auto_reject_threshold:
        return "reject"
    elif score <= auto_approve_threshold:
        return "approve"
    else:
        return "review"


if __name__ == "__main__":
    port = int(os.getenv("PORT", "5000"))
    debug = os.getenv("DEBUG", "false").lower() == "true"
    
    logger.info(f"Starting content moderation service on port {port}")
    app.run(host="0.0.0.0", port=port, debug=debug)
