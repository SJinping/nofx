import { useRef, useEffect, useState } from 'react';

interface RangeSliderProps {
  min: number;
  max: number;
  leftValue: number;
  rightValue: number;
  onLeftChange: (value: number) => void;
  onRightChange: (value: number) => void;
  step?: number;
}

export function RangeSlider({
  min,
  max,
  leftValue,
  rightValue,
  onLeftChange,
  onRightChange,
  step = 1,
}: RangeSliderProps) {
  const trackRef = useRef<HTMLDivElement>(null);
  const [isDraggingLeft, setIsDraggingLeft] = useState(false);
  const [isDraggingRight, setIsDraggingRight] = useState(false);

  // 计算滑块位置百分比
  const getPercentage = (value: number) => {
    if (max <= min) return 0;
    return ((value - min) / (max - min)) * 100;
  };

  // 从鼠标位置计算值
  const getValueFromMouseEvent = (e: MouseEvent) => {
    if (!trackRef.current) return min;
    
    const rect = trackRef.current.getBoundingClientRect();
    const x = e.clientX - rect.left;
    const percentage = Math.max(0, Math.min(100, (x / rect.width) * 100));
    const value = min + (percentage / 100) * (max - min);
    
    // 对齐到步长
    return Math.round(value / step) * step;
  };

  // 左滑块拖动
  useEffect(() => {
    if (!isDraggingLeft) return;

    const handleMouseMove = (e: MouseEvent) => {
      const newValue = getValueFromMouseEvent(e);
      // 确保左滑块不超过右滑块
      if (newValue < rightValue) {
        onLeftChange(Math.max(min, newValue));
      }
    };

    const handleMouseUp = () => {
      setIsDraggingLeft(false);
    };

    document.addEventListener('mousemove', handleMouseMove);
    document.addEventListener('mouseup', handleMouseUp);

    return () => {
      document.removeEventListener('mousemove', handleMouseMove);
      document.removeEventListener('mouseup', handleMouseUp);
    };
  }, [isDraggingLeft, rightValue, min, onLeftChange, step]);

  // 右滑块拖动
  useEffect(() => {
    if (!isDraggingRight) return;

    const handleMouseMove = (e: MouseEvent) => {
      const newValue = getValueFromMouseEvent(e);
      // 确保右滑块不超过左滑块
      if (newValue > leftValue) {
        onRightChange(Math.min(max, newValue));
      }
    };

    const handleMouseUp = () => {
      setIsDraggingRight(false);
    };

    document.addEventListener('mousemove', handleMouseMove);
    document.addEventListener('mouseup', handleMouseUp);

    return () => {
      document.removeEventListener('mousemove', handleMouseMove);
      document.removeEventListener('mouseup', handleMouseUp);
    };
  }, [isDraggingRight, leftValue, max, onRightChange, step]);

  const leftPercentage = getPercentage(leftValue);
  const rightPercentage = getPercentage(rightValue);

  return (
    <div className="px-4 py-6">
      {/* 显示当前范围 */}
      <div className="flex items-center justify-between mb-3">
        <div className="text-xs" style={{ color: '#848E9C' }}>
          数据范围: <span className="font-semibold mono" style={{ color: '#EAECEF' }}>{leftValue}</span>
          {' '}-{' '}
          <span className="font-semibold mono" style={{ color: '#EAECEF' }}>{rightValue}</span>
          {' '}({rightValue - leftValue + 1} 个点)
        </div>
        <button
          onClick={() => {
            onLeftChange(min);
            onRightChange(max);
          }}
          className="text-xs px-2 py-1 rounded hover:bg-opacity-80 transition-all"
          style={{ 
            background: 'rgba(240, 185, 11, 0.1)', 
            color: '#F0B90B',
            border: '1px solid rgba(240, 185, 11, 0.2)'
          }}
        >
          重置
        </button>
      </div>

      {/* 滑块轨道 */}
      <div className="relative h-2 select-none" ref={trackRef}>
        {/* 背景轨道 */}
        <div
          className="absolute w-full h-full rounded-full"
          style={{ background: '#2B3139' }}
        />

        {/* 选中区域 */}
        <div
          className="absolute h-full rounded-full"
          style={{
            left: `${leftPercentage}%`,
            right: `${100 - rightPercentage}%`,
            background: 'linear-gradient(90deg, #F0B90B 0%, #FCD535 100%)',
          }}
        />

        {/* 左滑块 */}
        <div
          className="absolute top-1/2 -translate-y-1/2 -translate-x-1/2 cursor-grab active:cursor-grabbing"
          style={{
            left: `${leftPercentage}%`,
          }}
          onMouseDown={(e) => {
            e.preventDefault();
            setIsDraggingLeft(true);
          }}
        >
          <div
            className="w-5 h-5 rounded-full border-2 transition-all duration-150"
            style={{
              background: isDraggingLeft ? '#F0B90B' : '#1E2329',
              borderColor: '#F0B90B',
              boxShadow: isDraggingLeft 
                ? '0 0 0 4px rgba(240, 185, 11, 0.2), 0 2px 8px rgba(240, 185, 11, 0.4)' 
                : '0 2px 4px rgba(0, 0, 0, 0.3)',
            }}
          />
        </div>

        {/* 右滑块 */}
        <div
          className="absolute top-1/2 -translate-y-1/2 -translate-x-1/2 cursor-grab active:cursor-grabbing"
          style={{
            left: `${rightPercentage}%`,
          }}
          onMouseDown={(e) => {
            e.preventDefault();
            setIsDraggingRight(true);
          }}
        >
          <div
            className="w-5 h-5 rounded-full border-2 transition-all duration-150"
            style={{
              background: isDraggingRight ? '#F0B90B' : '#1E2329',
              borderColor: '#F0B90B',
              boxShadow: isDraggingRight 
                ? '0 0 0 4px rgba(240, 185, 11, 0.2), 0 2px 8px rgba(240, 185, 11, 0.4)' 
                : '0 2px 4px rgba(0, 0, 0, 0.3)',
            }}
          />
        </div>
      </div>
    </div>
  );
}

