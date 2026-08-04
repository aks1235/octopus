'use client';

import { Suspense } from 'react';
import { CONTENT_MAP } from './config';

function ModuleSkeleton() {
    return (
        <div className="flex flex-col gap-4 p-4 animate-in fade-in duration-200">
            <div className="h-10 bg-muted/50 rounded-lg w-64 animate-pulse" />
            <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
                {Array.from({ length: 6 }).map((_, i) => (
                    <div key={i} className="h-40 bg-muted/50 rounded-xl animate-pulse" />
                ))}
            </div>
        </div>
    );
}

export function ContentLoader({ activeRoute }: { activeRoute: string }) {
    const Component = CONTENT_MAP[activeRoute];

    if (!Component) {
        return (
            <div className="flex items-center justify-center h-64">
                <p className="text-muted-foreground">Route not found: {activeRoute}</p>
            </div>
        );
    }

    return (
        <Suspense fallback={<ModuleSkeleton />}>
            <Component />
        </Suspense>
    );
}
