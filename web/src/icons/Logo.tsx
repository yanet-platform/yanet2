import React from 'react';

interface LogoProps extends React.SVGProps<SVGSVGElement> {
    size?: number;
}

const Logo = ({ size = 32, ...props }: LogoProps): React.JSX.Element => {
    return (
        <svg
            xmlns="http://www.w3.org/2000/svg"
            width={size}
            height={size}
            viewBox="0 0 32 32"
            {...props}
        >
            <g id="surface1">
                <path
                    style={{
                        stroke: 'none',
                        fillRule: 'nonzero',
                        fill: 'currentColor',
                        fillOpacity: 1,
                    }}
                    d="M 26.691406 11.023438 C 25.433594 12.703125 22.246094 17.179688 20.027344 22.023438 C 18.410156 25.550781 17.640625 29.382812 17.269531 32 C 24.101562 30.453125 28.933594 23.992188 28.34375 16.71875 C 28.1875 14.660156 27.601562 12.738281 26.691406 11.023438 Z M 26.691406 11.023438 "
                />
                <path
                    style={{
                        stroke: 'none',
                        fillRule: 'nonzero',
                        fill: 'currentColor',
                        fillOpacity: 1,
                    }}
                    d="M 15.054688 15.34375 C 20.121094 12.96875 22.230469 9.382812 23.105469 6.65625 C 20.363281 4.40625 16.789062 3.175781 12.992188 3.492188 C 6.039062 4.0625 0.660156 9.636719 0 16.449219 C 1.941406 17.191406 7.71875 18.773438 15.054688 15.34375 Z M 15.054688 15.34375 "
                />
                <path
                    style={{
                        stroke: 'none',
                        fillRule: 'nonzero',
                        fill: 'currentColor',
                        fillOpacity: 1,
                    }}
                    d="M 23.585938 11.144531 C 21.550781 13.238281 18.242188 16.046875 13.792969 17.945312 C 8.171875 20.355469 2.886719 21.195312 0.359375 21.476562 C 1.390625 25.585938 4.136719 28.933594 7.71875 30.785156 C 9.335938 29.640625 19.535156 22.035156 23.585938 11.144531 Z M 23.585938 11.144531 "
                />
                <path
                    style={{
                        stroke: 'none',
                        fillRule: 'nonzero',
                        fill: 'currentColor',
                        fillOpacity: 1,
                    }}
                    d="M 32 3.429688 C 32 1.535156 30.488281 0 28.621094 0 C 26.753906 0 25.242188 1.535156 25.242188 3.429688 C 25.242188 5.324219 26.753906 6.863281 28.621094 6.863281 C 30.488281 6.863281 32 5.324219 32 3.429688 Z M 32 3.429688 "
                />
            </g>
        </svg>
    );
};

export default Logo;

