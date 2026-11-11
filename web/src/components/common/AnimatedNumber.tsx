import { useEffect, useState, useRef } from 'react';
import { animate } from 'motion/react';

interface AnimatedNumberProps {
    value: string | number | undefined;
    duration?: number;
    // 小数位。缺省沿用旧行为: 字符串带小数点显示两位, 纯整数不显示小数。
    // 百分比、单价这类需要一位或三位小数, 由调用方显式指定, 否则会出现 95.90%、2.00 这种写法。
    digits?: number;
}

export function AnimatedNumber({ value, duration = 800, digits }: AnimatedNumberProps) {
    const [displayValue, setDisplayValue] = useState(0);
    const prevValueRef = useRef(0);

    useEffect(() => {
        if (value === undefined || value === null || value === '-') {
            prevValueRef.current = 0;
            return;
        }

        const numericValue = typeof value === 'string'
            ? parseFloat(value.replace(/,/g, ''))
            : value;

        if (isNaN(numericValue)) {
            return;
        }

        const controls = animate(prevValueRef.current, numericValue, {
            duration: duration / 1000, // motion/react uses seconds
            ease: 'easeOut',
            onUpdate: (latest) => {
                setDisplayValue(latest);
                prevValueRef.current = latest;
            }
        });

        return () => controls.stop();
    }, [value, duration]);

    if (value === undefined || value === null) {
        return <span>-</span>;
    }

    const shouldShowDecimals = typeof value === 'string' && value.includes('.');
    const decimalPlaces = digits ?? (shouldShowDecimals ? 2 : 0);

    const formattedValue = displayValue.toLocaleString('en-US', {
        minimumFractionDigits: decimalPlaces,
        maximumFractionDigits: decimalPlaces
    });

    return <span>{formattedValue}</span>;
}