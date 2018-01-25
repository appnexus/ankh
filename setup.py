#!/usr/bin/env python

from setuptools import setup, find_packages

install_requires = [
    'colorlog==2.7.0',
    'requests==2.18.4',
    'pyyaml==3.12',
]

setup(
    name='ankh',
    version="0.0.1",
    description='',
    install_requires=install_requires,
    packages=find_packages(include=['ankh']),
    entry_points={
        'console_scripts': [
            'ankh = ankh.cli:main'
        ]
    },
)
