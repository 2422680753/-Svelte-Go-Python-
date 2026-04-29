import re
import jieba
from typing import List, Dict, Any, Tuple
from dataclasses import dataclass
import logging

logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)


@dataclass
class TextAnalysisResult:
    text: str
    score: float
    violations: List[str]
    keywords: List[str]
    details: Dict[str, Any]


class TextAnalyzer:
    def __init__(self):
        self.sensitive_keywords = {
            "violence": [
                "暴力", "血腥", "打人", "打架", "斗殴", "杀人", "凶杀",
                "刀", "枪", "武器", "炸弹", "爆炸", "攻击", "殴打", "打",
                "杀", "死", "砍", "捅", "射", "炸", "烧", "焚",
                "violence", "kill", "murder", "fight", "beat", "stab",
                "shoot", "bomb", "explode", "attack", "weapon", "gun", "knife"
            ],
            "pornography": [
                "色情", "淫秽", "裸", "裸体", "裸体照", "性", "性交",
                "做爱", "约炮", "嫖娼", "卖淫", "口交", "肛交", "手淫",
                "自慰", "AV", "黄片", "三级片", "色情片", "成人片",
                "porn", "sex", "nude", "naked", "adult", "xxx", "fuck",
                "pornography", "masturbate", "intercourse"
            ],
            "hate_speech": [
                "傻逼", "草泥马", "妈逼", "操你", "傻逼", "滚蛋", "去死",
                "白痴", "笨蛋", "废物", "垃圾", "人渣", "杂种", "混蛋",
                "黑鬼", "中国猪", "支那", "卖国贼", "汉奸", "台独",
                "港独", "疆独", "藏独", "反共", "反华",
                "nigger", "chink", "kike", "spic", "faggot", "retard",
                "hate", "racist", "racism", "discriminate"
            ],
            "gambling": [
                "赌博", "赌球", "赌马", "赌场", "下注", "赌债", "庄家",
                "六合彩", "时时彩", "老虎机", "牌九", "骰子", "麻将",
                "扑克", "炸金花", "斗地主", "跑得快", "赌博游戏",
                "gamble", "casino", "bet", "betting", "lottery", "poker",
                "blackjack", "roulette", "slots"
            ],
            "drugs": [
                "毒品", "可卡因", "海洛因", "大麻", "冰毒", "摇头丸",
                "K粉", "麻古", "咖啡因", "安非他命", "芬太尼", "吸毒",
                "贩毒", "走私毒品", "非法持有毒品", "注射", "吸食",
                "drug", "cocaine", "heroin", "marijuana", "cannabis",
                "meth", "amphetamine", "fentanyl", "addict", "narcotic"
            ],
            "politics": [
                "共产党", "国民党", "民进党", "法轮功", "六四", "天安门",
                "胡锦涛", "温家宝", "江泽民", "习近平", "李克强",
                "毛泽东", "邓小平", "陈云", "薄熙来", "周永康",
                "台独", "港独", "疆独", "藏独", "自由民主",
                "政治改革", "人权", "言论自由", "新闻自由"
            ],
            "fraud": [
                "诈骗", "欺诈", "骗钱", "非法集资", "传销", "直销",
                "虚假宣传", "假冒伪劣", "诈骗电话", "短信诈骗",
                "网络诈骗", "电信诈骗", "中奖诈骗", "刷单", "刷信誉",
                "fraud", "scam", "cheat", "deceive", "pyramid",
                "ponzi", "fake", "counterfeit"
            ]
        }
        
        self.sensitive_patterns = [
            (r'微信.*?[0-9a-zA-Z]{5,}', "wechat_contact"),
            (r'vx.*?[0-9a-zA-Z]{5,}', "wechat_contact"),
            (r'加.*?[微信|vx|V]', "contact_info"),
            (r'联系.*?[微信|vx|电话|手机]', "contact_info"),
            (r'http[s]?://(?:[a-zA-Z]|[0-9]|[$-_@.&+]|[!*\(\),]|(?:%[0-9a-fA-F][0-9a-fA-F]))+', "external_link"),
            (r'(?:日|操|草|干|死)你?(?:妈|娘|全家|祖宗)', "curse_word"),
            (r'(?:傻|煞|撒)(?:逼|B|笔|比)', "curse_word"),
            (r'妈(?:逼|B|比)', "curse_word"),
        ]
        
        self.regex_patterns = {}
        for category, keywords in self.sensitive_keywords.items():
            pattern = '|'.join([re.escape(k) for k in keywords])
            self.regex_patterns[category] = re.compile(pattern, re.IGNORECASE)
            
    def analyze(self, text: str) -> TextAnalysisResult:
        """分析文本内容"""
        if not text or not text.strip():
            return TextAnalysisResult(
                text="",
                score=0.0,
                violations=[],
                keywords=[],
                details={"empty_text": True}
            )
            
        text_lower = text.lower()
        total_score = 0.0
        violations = []
        matched_keywords = []
        category_scores = {}
        
        for category, pattern in self.regex_patterns.items():
            matches = pattern.findall(text_lower)
            if matches:
                unique_matches = list(set(matches))
                matched_keywords.extend(unique_matches)
                
                score = min(0.9, len(unique_matches) * 0.3)
                category_scores[category] = score
                
                if score > 0.5:
                    violations.append(category)
                    
                total_score = max(total_score, score)
                
        pattern_matches = []
        for pattern, name in self.sensitive_patterns:
            matches = re.findall(pattern, text_lower)
            if matches:
                pattern_matches.append({
                    "name": name,
                    "matches": matches
                })
                
                score = 0.3
                if name in ["curse_word", "wechat_contact"]:
                    score = 0.5
                    
                total_score = max(total_score, score)
                
        word_list = list(jieba.cut(text))
        word_freq = {}
        for word in word_list:
            if len(word) > 1:
                word_freq[word] = word_freq.get(word, 0) + 1
                
        sorted_words = sorted(word_freq.items(), key=lambda x: x[1], reverse=True)[:10]
        
        details = {
            "category_scores": category_scores,
            "pattern_matches": pattern_matches,
            "word_count": len(word_list),
            "char_count": len(text),
            "top_words": [w[0] for w in sorted_words]
        }
        
        if total_score > 0.7:
            severity = "high"
        elif total_score > 0.4:
            severity = "medium"
        else:
            severity = "low"
            
        details["severity"] = severity
        
        return TextAnalysisResult(
            text=text,
            score=total_score,
            violations=violations,
            keywords=list(set(matched_keywords)),
            details=details
        )
        
    def analyze_multiple(self, texts: List[str]) -> List[TextAnalysisResult]:
        """批量分析多个文本"""
        results = []
        for text in texts:
            result = self.analyze(text)
            results.append(result)
        return results
        
    def get_overall_score(self, results: List[TextAnalysisResult]) -> Tuple[float, List[str], List[str]]:
        """计算多个文本分析结果的综合得分"""
        if not results:
            return 0.0, [], []
            
        max_score = max([r.score for r in results])
        
        all_violations = set()
        all_keywords = set()
        
        for r in results:
            all_violations.update(r.violations)
            all_keywords.update(r.keywords)
            
        return max_score, list(all_violations), list(all_keywords)
