import pandas as pd
import re
import matplotlib.pyplot as plt

def process_gpu_data(file_path):
    # 存储结果的字典 {index: value}
    results = {}

    # 正则表达式说明：
    # GPC_(\d+) -> 匹配 GPC 并捕获其编号
    # SA_(\d+)  -> 匹配 SA 并捕获其编号
    # CaPWQL1VCache_(\d+) -> 匹配 Cache 并捕获其编号

    try:
        # 读取 CSV，假设没有表头，或者数据在第一列
        # 如果你的 CSV 结构复杂，可以调整 read_csv 的参数
        df = pd.read_csv(file_path, header=0)

        for index, row in df.iterrows():
            where = str(row.iloc[1])  # 假设关键字在第一列
            what = str(row.iloc[2])   # 假设数值在第二列
            # 1. 检查关键字匹配
            print(what)
            # if "CaPWQL1VCache" in where and "ptw-read-" in what:
            #     GPC = where.strip().split('.')[2][5]
            #     SA = where.strip().split('.')[3][4]
            #     Cache = where.strip().split('.')[4][-1]
                
            #     gpc_id = int(GPC)
            #     sa_id = int(SA)
            #     cache_id = int(Cache)
                
            #     # 3. 提取最后的数值 (假设数值在最后一部分)
            #     # 示例数据最后一部分是 2004.000000000000
            #     value = float(str(row.iloc[-1]).strip())
                
            #     # 4. 计算 Index: GPC*16 + SA*4 + CaPWQL1VCache
            #     calc_index = gpc_id * 16 + sa_id * 4 + cache_id
            #     results[calc_index] = value
            if "CaPWQL1ICache" in where and "ptw-read-" in what:
                GPC = where.strip().split('.')[2][5]
                SA = where.strip().split('.')[3][4]
                
                gpc_id = int(GPC)
                sa_id = int(SA)
                
                # 3. 提取最后的数值 (假设数值在最后一部分)
                # 示例数据最后一部分是 2004.000000000000
                value = float(str(row.iloc[-1]).strip())
                
                # 4. 计算 Index: GPC*16 + SA*4 + CaPWQL1VCache
                calc_index = gpc_id * 4 + sa_id
                results[calc_index] = value

        if not results:
            print("未发现匹配的数据。")
            return
        
        print(results)

        # 5. 绘图准备
        sorted_indices = sorted(results.keys())
        sorted_values = [results[i] for i in sorted_indices]

        plt.figure(figsize=(12, 6))
        bars = plt.bar(sorted_indices, sorted_values, color='skyblue', edgecolor='navy')
        
        plt.xlabel('Calculated Index (GPC*16 + SA*4 + Cache)')
        plt.ylabel('Value')
        plt.title('GPU Cache Metrics Analysis')
        plt.grid(axis='y', linestyle='--', alpha=0.7)

        # 在柱状图上显示具体数值（可选）
        for bar in bars:
            yval = bar.get_height()
            plt.text(bar.get_x() + bar.get_width()/2, yval, int(yval), va='bottom', ha='center', fontsize=8)

        plt.show()
        plt.savefig('../../figures/gpu_cache_mshr_distribution.png')  # 保存图像到当前目录

    except Exception as e:
        print(f"处理文件时出错: {e}")

# 执行脚本
process_gpu_data('/Users/chenzihang/Develop/CachePWQ/simulator/mgpusim/samples/matrixtranspose/metrics.csv')