import argparse
import pandas as pd
import re
import seaborn as sns
import matplotlib.pyplot as plt
import matplotlib.font_manager as fm
import os

def process_gpu_logs(file_path):
    data = []
    
    # 正则表达式说明：
    # (\d+\.\d+): 匹配时间戳
    # (\d+): 匹配第二个数字元素
    # GPC_(\d+): 匹配 GPC 的 ID
    # MP_(\d+): 匹配 MP 的 ID
    pattern = re.compile(r'(\d+\.\d+),\s*(\d+),.*GPC_(\d+).*MP_(\d+)')

    with open(file_path, 'r') as f:
        for line in f:
            match = pattern.search(line)
            if match:
                # 提取原始数据
                timestamp = float(match.group(1))
                flag = int(match.group(2))
                gpc_id = int(match.group(3))
                mp_id = int(match.group(4))
                
                # 步骤 2: 过滤第二个元素不为 1 的记录
                if flag == 0:
                    # 步骤 4: 第一个元素乘 1e9 (转换为纳秒单位)
                    timestamp_ns = timestamp * 1e9
                    data.append([timestamp_ns, gpc_id, mp_id])

    # 步骤 1: 记录在 DataFrame 中
    df = pd.DataFrame(data, columns=['Timestamp_ns', 'GPC_ID', 'MP_ID'])
    return df

def plot_latency_heatmap(
    df: pd.DataFrame,
    out_dir: str,
):
    # 1. 计算 GPC 到 MP 的平均延迟
    # 这里假设你已经按要求把第一个元素乘了 1e9 得到纳秒
    if not os.path.exists(out_dir):
        os.makedirs(out_dir)

    # Set Arial font family
    plt.rcParams["font.family"] = "Arial"
    # For macOS, you might need to explicitly set the font file
    plt.rcParams["font.sans-serif"] = ["Arial"]
    plt.rcParams["mathtext.fontset"] = "custom"
    plt.rcParams["mathtext.rm"] = "Arial"
    plt.rcParams["mathtext.it"] = "Arial:italic"
    plt.rcParams["mathtext.bf"] = "Arial:bold"

    pivot_table = df.pivot_table(
        values='Timestamp_ns', # 如果是计算差值，请替换为你的延迟列名
        index='MP_ID', 
        columns='GPC_ID', 
        aggfunc='mean'
    )

    # 2. 绘图设置
    plt.figure(figsize=(12, 8), dpi=300)
    heatmap = sns.heatmap(
        pivot_table, 
        annot=True,      # 在格子内显示具体数值
        fmt=".0f",       # 保留一位小数
        cmap="YlGnBu",   # 颜色渐变方案：黄-绿-蓝
        annot_kws={"size": 20, "weight": "bold"},
        cbar_kws={
            'label': 'Average Latency (Cycles)', 
            'location': 'left',
            'pad': 0.125  # 调整这个值可以控制 colorbar 离热力图的距离
        }
    )

    plt.xticks(fontsize=22, fontweight='bold')
    plt.yticks(fontsize=22, fontweight='bold')

    font_prop = fm.FontProperties(family='Arial', size=24, weight='bold')
    cbar = heatmap.collections[0].colorbar
    cbar.ax.tick_params(labelsize=24)  # 刻度字体大小
    cbar.ax.yaxis.label.set_fontproperties(font_prop)  # 标签字体

    plt.ylabel('Memory Partition ID', fontsize=24, fontweight='bold')
    plt.xlabel('GPC ID', fontsize=24, fontweight='bold')

    ax = plt.gca()

    # 设置图的边框加粗
    for spine in ax.spines.values():
        spine.set_visible(True)      # 确保可见
        spine.set_linewidth(1.75)     # 增加宽度到 2.5 观察效果
        spine.set_edgecolor('black') # 设置为黑色确保对比度

    # 3. 特别注意：Seaborn 热力图有时会隐藏 top 和 right 的 spine
    # 显式开启它们
    ax.spines['top'].set_visible(True)
    ax.spines['right'].set_visible(True)
    ax.spines['left'].set_visible(True)
    ax.spines['bottom'].set_visible(True)

    cbar = heatmap.collections[0].colorbar
    cbar.outline.set_linewidth(1.75)
    cbar.outline.set_edgecolor('black')
    
    # 解决中文显示问题（如果标题需要中文）
    # plt.rcParams['font.sans-serif'] = ['SimHei'] 
    
    output_file = os.path.join(
        out_dir, "gpc_mp_latency_heatmap"
    )
    plt.savefig(output_file + ".png")
    plt.savefig(output_file + ".pdf")
    print(f"Plot saved to {output_file}")

if __name__ == "__main__":
    # Example usage
    parser = argparse.ArgumentParser(description="Parse csv file.")

    parser.add_argument(
        "--outDir",
        required=True,
        type=str,
        help="Directory path to save the output plots.",
    )

    args = parser.parse_args()

    # 使用脚本
    file_path = '/Users/chenzihang/Develop/CachePWQ/data/GPC_MP_latency/log'  # 你的日志文件名
    df_result = process_gpu_logs(file_path)

    # 查看结果
    print(f"处理完成，共有 {len(df_result)} 条有效记录。")

    # 调用函数
    plot_latency_heatmap(df_result, args.outDir)