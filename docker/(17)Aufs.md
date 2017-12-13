# Aufs

例子参考于[自己动手写docker](https://github.com/xianlubird/mydocker)，演示aufu＋Cow技术
## 环境
```shell
root@fqhnode:~/aufs# uname -a
Linux fqhnode 4.4.0-87-generic #110-Ubuntu SMP Tue Jul 18 12:55:35 UTC 2017 x86_64 x86_64 x86_64 GNU/Linux
```

## 创建好各个layer
目录结构如下，其中mnt是挂载点
```shell
[root@fqhnode01 aufs]# tree ./
./
├── mnt
├── ro-layer-1
│   └── ro-1.txt
├── ro-layer-2
│   └── ro-2.txt
└── rw-layer
    └── rw.txt
```

## 联合挂载
```shell
mount -t aufs -o dirs=./rw-layer/:./ro-layer-2:./ro-layer-1 none ./mnt/
```
效果如下:
1. 从用户的视角来看，文件系统将从./mnt开始，其实./mnt只是一个虚拟挂载点，里面并没有任何实质性数据。
```shell
root@fqhnode:~/aufs# tree ./mnt/
./mnt/
|-- ro-1.txt
|-- ro-2.txt
`-- rw.txt
```

2. 在./mnt目录下新建一个文件new-file.txt
```shell
root@fqhnode:~/aufs# tree ./
./
|-- mnt
|   |-- new-file.txt
|   |-- ro-1.txt
|   |-- ro-2.txt
|   `-- rw.txt
|-- ro-layer-1
|   `-- ro-1.txt
|-- ro-layer-2
|   `-- ro-2.txt
`-- rw-layer
    |-- new-file.txt
    `-- rw.txt
```

3. 对./mnt目录下任何内容进行更改，系统都会在第一次更改的时候copy一份对应的ro-{n}.txt到目录rw-layer下，在副本下进行修改。 而不是直接修改ro-layer的数据。
```shell
root@fqhnode:~/aufs# cat mnt/ro-1.txt 
I am ro-1 layer!
new write!
root@fqhnode:~/aufs# cat rw-layer/ro-1.txt 
I am ro-1 layer!
new write!
root@fqhnode:~/aufs# cat ro-layer-1/ro-1.txt 
I am ro-1 layer!
```


